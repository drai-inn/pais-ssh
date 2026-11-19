package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHost(t *testing.T) {
	for _, tc := range []struct{ host, env, name, project string }{
		{"drai-test.my-workspace", "drai-test", "my-workspace", ""},
		{"pais-dev.shared-ws.team-project", "pais-dev", "shared-ws", "team-project"},
	} {
		e, name, project, err := parseHost(tc.host)
		if err != nil || e.name != tc.env || name != tc.name || project != tc.project {
			t.Errorf("parseHost(%q) = %q %q %q %v", tc.host, e.name, name, project, err)
		}
	}
	for _, host := range []string{"nope.my-workspace", "drai-test", "drai-test."} {
		if _, _, _, err := parseHost(host); err == nil {
			t.Errorf("parseHost(%q): no error", host)
		}
	}
}

// Each environment replaces only its own block; the user's own settings stay below the blocks.
func TestWriteBlockKeepsOtherEnvironments(t *testing.T) {
	test, _ := findEnv("drai-test")
	dev, _ := findEnv("pais-dev")
	cfg := filepath.Join(t.TempDir(), "config")
	os.WriteFile(cfg, []byte("Host *\n  ServerAliveCountMax 3\n"), 0o600)

	write := func(e env, hosts ...string) {
		if err := writeBlock(e, cfg, block(e, "/bin/pais-ssh", "me", hosts)); err != nil {
			t.Fatal(err)
		}
	}
	write(test, "drai-test.a")
	write(dev, "pais-dev.b")
	write(test, "drai-test.c")

	b, _ := os.ReadFile(cfg)
	got := string(b)
	for _, want := range []string{"Host drai-test.c\n", "Host pais-dev.b\n", "Host drai-test.*\n", "Host pais-dev.*\n",
		"Host *\n  ServerAliveCountMax 3\n"} {
		if strings.Count(got, want) != 1 {
			t.Errorf("want %q once in:\n%s", want, got)
		}
	}
	if strings.Count(got, "  ControlPersist no\n") != 2 {
		t.Errorf("want ControlPersist no in each block:\n%s", got)
	}
	if strings.Contains(got, "drai-test.a") {
		t.Errorf("old drai-test block left behind:\n%s", got)
	}
}

func TestWorkspaceUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Authorization") != "Bearer tok":
			http.Error(w, "no", http.StatusForbidden)
		case r.URL.Path == "/p/new/url-templt-4/user":
			fmt.Fprintln(w, "jdoe001")
		default:
			http.Error(w, "websocket expected", http.StatusBadRequest) // websocat without the /user page
		}
	}))
	defer srv.Close()
	ws := func(name, phase string) workload {
		w := workload{Name: name, Phase: phase}
		w.Environments = append(w.Environments, struct {
			Connections []connection `json:"connections"`
		}{[]connection{{"SSH (client only)", srv.URL + "/p/" + name + "/url-templt-4"}}})
		return w
	}
	if got := workspaceUser("tok", []workload{ws("old", "Running"), ws("new", "Stopped")}); got != "" {
		t.Errorf("no running Workspace with /user: got %q", got)
	}
	if got := workspaceUser("tok", []workload{ws("old", "Running"), ws("new", "Running")}); got != "jdoe001" {
		t.Errorf("got %q, want jdoe001", got)
	}
	if got := workspaceUser("bad", []workload{ws("new", "Running")}); got != "" {
		t.Errorf("refused token: got %q", got)
	}
}

func TestSSHURLs(t *testing.T) {
	ws := func(names ...string) workload {
		var cs []connection
		for _, n := range names {
			cs = append(cs, connection{n, "https://x/" + n})
		}
		w := workload{}
		w.Environments = append(w.Environments, struct {
			Connections []connection `json:"connections"`
		}{cs})
		return w
	}
	for _, tc := range []struct {
		w     workload
		urls  int
		named bool
	}{
		{ws("Terminal", "SSH (client only)"), 1, true},
		{ws("templt-0", "templt-1", "templt-2"), 3, false},
		{ws("Terminal", "templt-1"), 0, false},
		{ws(), 0, false},
	} {
		if urls, named := tc.w.sshURLs(); len(urls) != tc.urls || named != tc.named {
			t.Errorf("%v: got %v %v", tc.w.Environments, urls, named)
		}
	}
}

// Of connections without names, the SSH one is the one whose /user gives a login name; the others give pages.
func TestFindSSHUnnamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/ws/url-3/user":
			fmt.Fprintln(w, "<!doctype html><html></html>")
		case "/p/ws/url-4/user":
			fmt.Fprintln(w, "jdoe001")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	urls := []string{srv.URL + "/p/ws", srv.URL + "/p/ws/url-3", srv.URL + "/p/ws/url-4"}
	if u, _ := findSSH("tok", urls, false); u != urls[2] {
		t.Errorf("got %q, want %q", u, urls[2])
	}
	if u, code := findSSH("tok", urls[:2], false); u != "" || code != http.StatusOK {
		t.Errorf("without the SSH URL: got %q %d", u, code)
	}
}
