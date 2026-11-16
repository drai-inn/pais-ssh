package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// oauthConfig uses Run:ai's own public Keycloak client, the one kubelogin uses, so the ID token it gives is accepted
// by both the Run:ai API and the Workspace's oauth2-proxy sidecar.
func (e env) oauthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:   e.issuer + "/protocol/openid-connect/auth",
			TokenURL:  e.issuer + "/protocol/openid-connect/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
		Scopes: []string{"openid", "email"},
	}
}

// loginPorts are kubelogin's, so the same `ssh -L` forward serves both. Keycloak accepts http://localhost on any
// port for this client, but not http://127.0.0.1.
var loginPorts = []string{"8000", "18000"}

type session struct {
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

type claims struct {
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Exp               int64  `json:"exp"`
}

// username is the Workspace login name: the University username, preferred_username without its domain.
func (c claims) username() string {
	u, _, _ := strings.Cut(c.PreferredUsername, "@")
	return u
}

// sessionPath is one file per environment.
func sessionPath(e env) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pais-ssh", e.name+".json"), nil
}

func loadSession(e env) (*session, error) {
	p, err := sessionPath(e)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s session
	return &s, json.Unmarshal(b, &s)
}

// saveSession replaces the file in one rename, since VS Code runs several proxies at once and each may refresh.
func saveSession(e env, s *session) error {
	p, err := sessionPath(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), "session-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

func logout(e env) error {
	p, err := sessionPath(e)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func parseClaims(jwt string) (claims, error) {
	var c claims
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return c, errors.New("malformed token")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

// token returns a current ID token, refreshing it when it has less than a minute left. It never opens a browser:
// as a ProxyCommand it runs under ssh or VS Code, where nobody would see the prompt.
func token(e env) (string, claims, error) {
	notSignedIn := fmt.Errorf("not signed in to %s, or the sign-in has expired: run `pais-ssh login --env %s`", e.name, e.name)
	s, err := loadSession(e)
	if err != nil {
		return "", claims{}, notSignedIn
	}
	if c, err := parseClaims(s.IDToken); err == nil && time.Until(time.Unix(c.Exp, 0)) > time.Minute {
		return s.IDToken, c, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := e.oauthConfig().TokenSource(ctx, &oauth2.Token{RefreshToken: s.RefreshToken}).Token()
	if err != nil {
		return "", claims{}, notSignedIn
	}
	s, err = sessionFrom(tok)
	if err != nil {
		return "", claims{}, err
	}
	if err := saveSession(e, s); err != nil {
		return "", claims{}, err
	}
	c, err := parseClaims(s.IDToken)
	return s.IDToken, c, err
}

func sessionFrom(tok *oauth2.Token) (*session, error) {
	id, _ := tok.Extra("id_token").(string)
	if id == "" {
		return nil, errors.New("the sign-in returned no ID token")
	}
	return &session{IDToken: id, RefreshToken: tok.RefreshToken}, nil
}

// login runs the authorisation code flow with PKCE, receiving the code on http://localhost:8000 (or 18000).
func login(e env, noBrowser bool) (claims, error) {
	var ln net.Listener
	var err error
	conf := e.oauthConfig()
	for _, port := range loginPorts {
		if ln, err = net.Listen("tcp", "127.0.0.1:"+port); err == nil {
			conf.RedirectURL = "http://localhost:" + port
			break
		}
	}
	if err != nil {
		return claims{}, fmt.Errorf("can't listen on localhost port %s for the sign-in: %w",
			strings.Join(loginPorts, " or "), err)
	}
	verifier := oauth2.GenerateVerifier()
	state := oauth2.GenerateVerifier()
	authURL := conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))

	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.NotFound(w, r)
			return
		}
		var res result
		if e := q.Get("error"); e != "" {
			res.err = fmt.Errorf("sign-in failed: %s %s", e, q.Get("error_description"))
			fmt.Fprintln(w, "Sign-in failed. See the terminal.")
		} else {
			res.code = q.Get("code")
			fmt.Fprintln(w, "Signed in to pais-ssh. You can close this tab.")
		}
		select {
		case done <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Fprintf(os.Stderr, "Sign in at:\n\n  %s\n\n", authURL)
	if !noBrowser {
		openBrowser(authURL)
	}

	var res result
	select {
	case res = <-done:
	case <-time.After(5 * time.Minute):
		return claims{}, errors.New("no sign-in within 5 minutes")
	}
	if res.err != nil {
		return claims{}, res.err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := conf.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return claims{}, fmt.Errorf("sign-in failed: %w", err)
	}
	s, err := sessionFrom(tok)
	if err != nil {
		return claims{}, err
	}
	if err := saveSession(e, s); err != nil {
		return claims{}, err
	}
	c, err := parseClaims(s.IDToken)
	if err != nil {
		return claims{}, err
	}
	fmt.Fprintf(os.Stderr, "Signed in to %s as %s.\n", e.name, c.Email)
	return c, nil
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	cmd.Start()
}
