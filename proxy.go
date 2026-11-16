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
		return errors.New("usage: pais-ssh proxy " + prefix + ".<workspace>[.<project>]")
	}
	name, project, err := parseHost(args[0])
	if err != nil {
		return err
	}
	tok, c, err := token()
	if err != nil {
		return err
	}

	all, err := workspaces(tok, c.Email, name)
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
			name, strings.Join(projects, ", "), prefix, name)
	}
	w := ws[0]
	u := w.sshURL()

	if w.Phase != "Running" {
		if w.Phase == "Stopped" {
			log.Printf("starting %s...", name)
			if err := resume(tok, w.ID); err != nil {
				return err
			}
		} else {
			log.Printf("%s is %s; waiting for it...", name, w.Phase)
		}
		if err := waitReady(tok, u); err != nil {
			return err
		}
		log.Printf("%s is running.", name)
	}
	return pipe(tok, u)
}

// parseHost splits "<prefix>.<workspace>[.<project>]". Run:ai names are DNS labels, so they contain no dots.
func parseHost(host string) (name, project string, err error) {
	rest, ok := strings.CutPrefix(host, prefix+".")
	if !ok || rest == "" {
		return "", "", fmt.Errorf("%q isn't %s.<workspace>[.<project>]", host, prefix)
	}
	name, project, _ = strings.Cut(rest, ".")
	return name, project, nil
}

// waitReady polls the SSH URL until websocat answers a plain GET: 200 (the help page) or 400 (templates without it).
// Until then a starting Workspace gives 503.
func waitReady(tok, u string) error {
	client := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		code := 0
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
			code = resp.StatusCode
		}
		if code == http.StatusOK || code == http.StatusBadRequest {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the Workspace isn't reachable after 5 minutes (last HTTP %d); check it in Run:ai", code)
		}
		time.Sleep(3 * time.Second)
	}
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
