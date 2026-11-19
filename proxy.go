package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// proxy is the ssh ProxyCommand: it carries the SSH stream between stdin/stdout and the Workspace's SSH URL.
// stdin and stdout belong to ssh, so messages go to stderr only.
func proxy(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: pais-ssh proxy <env>.<workspace>[.<project>]")
	}
	e, name, project, err := parseHost(args[0])
	if err != nil {
		return err
	}
	tok, c, err := token(e)
	if err != nil {
		return err
	}

	all, err := workspaces(e, tok, c.Email, name)
	if err != nil {
		return err
	}
	var ws []workload
	var projects []string
	for _, w := range all {
		if project == "" || w.ProjectName == project {
			ws = append(ws, w)
			projects = append(projects, w.ProjectName)
		}
	}
	switch len(ws) {
	case 0:
		return fmt.Errorf("you have no Workspace %q with SSH", args[0])
	case 1:
	default:
		return fmt.Errorf("you have a Workspace %q in more than one project (%s): use %s.%s.<project>",
			name, strings.Join(projects, ", "), e.name, name)
	}
	w := ws[0]
	urls, named := w.sshURLs()
	u := ""
	if named {
		u = urls[0]
	}

	if w.Phase != "Running" {
		if w.Phase == "Stopped" {
			log.Printf("starting %s...", name)
			if err := resume(e, tok, w.ID); err != nil {
				return err
			}
		} else {
			log.Printf("%s is %s; waiting for it...", name, w.Phase)
		}
		if u, err = waitReady(tok, urls, named); err != nil {
			return err
		}
		log.Printf("%s is running.", name)
	} else if !named {
		if u, _ = findSSH(tok, urls, named); u == "" {
			return fmt.Errorf("%s's connections have lost their names (its template was edited in advanced setup) and none of them answers as SSH: make a new Workspace", name)
		}
	}
	return pipe(tok, u)
}

// parseHost splits "<env>.<workspace>[.<project>]". Environment and Run:ai names contain no dots.
func parseHost(host string) (e env, name, project string, err error) {
	envName, rest, ok := strings.Cut(host, ".")
	if !ok || rest == "" {
		return env{}, "", "", fmt.Errorf("%q isn't <env>.<workspace>[.<project>]", host)
	}
	if e, err = findEnv(envName); err != nil {
		return env{}, "", "", err
	}
	name, project, _ = strings.Cut(rest, ".")
	return e, name, project, nil
}

var probeClient = &http.Client{
	Timeout:       20 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// waitReady polls the Workspace until findSSH finds its SSH URL, and returns it. Until then a starting Workspace
// gives 503.
func waitReady(tok string, urls []string, named bool) (string, error) {
	deadline := time.Now().Add(5 * time.Minute)
	for {
		u, code := findSSH(tok, urls, named)
		if u != "" {
			return u, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("the Workspace isn't reachable after 5 minutes (last HTTP %d); check it in Run:ai", code)
		}
		time.Sleep(3 * time.Second)
	}
}

// findSSH gives the SSH URL among urls (sshURLs) once it answers, and the last HTTP status. A named one answers when
// websocat answers a plain GET: 200 (the help page) or 400 (templates without it). Of unnamed ones, the SSH URL is
// the one whose /user gives a login name.
func findSSH(tok string, urls []string, named bool) (string, int) {
	if named {
		code := 0
		req, err := http.NewRequest(http.MethodGet, urls[0], nil)
		if err != nil {
			return "", 0
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if resp, err := probeClient.Do(req); err == nil {
			resp.Body.Close()
			code = resp.StatusCode
		}
		if code == http.StatusOK || code == http.StatusBadRequest {
			return urls[0], code
		}
		return "", code
	}
	code := 0
	for _, u := range urls {
		var name string
		if name, code = askUser(tok, u); name != "" {
			return u, code
		}
	}
	return "", code
}

func pipe(tok, u string) error {
	ctx := context.Background()
	conn, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + tok}},
	})
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusFound, http.StatusUnauthorized, http.StatusForbidden:
				return fmt.Errorf("refused (HTTP %d): only the Workspace's owner gets in", resp.StatusCode)
			}
		}
		return fmt.Errorf("can't connect to the Workspace: %w", err)
	}
	defer conn.CloseNow()
	// websocat sends messages of up to 64 KiB, over the default limit of 32 KiB.
	conn.SetReadLimit(-1)
	nc := websocket.NetConn(ctx, conn, websocket.MessageBinary)

	go func() {
		io.Copy(nc, os.Stdin)
		nc.Close()
	}()
	_, err = io.Copy(os.Stdout, nc)
	return err
}
