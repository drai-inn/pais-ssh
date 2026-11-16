package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseHost(t *testing.T) {
	for _, tc := range []struct{ host, env, name, project string }{
		{"drai-test.cs-ssh3", "drai-test", "cs-ssh3", ""},
		{"pais-dev.mp-shell.matt-project", "pais-dev", "mp-shell", "matt-project"},
	} {
		e, name, project, err := parseHost(tc.host)
		if err != nil || e.name != tc.env || name != tc.name || project != tc.project {
			t.Errorf("parseHost(%q) = %q %q %q %v", tc.host, e.name, name, project, err)
		}
	}
	for _, host := range []string{"nope.cs-ssh3", "drai-test", "drai-test."} {
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
	if strings.Contains(got, "drai-test.a") {
		t.Errorf("old drai-test block left behind:\n%s", got)
	}
}
