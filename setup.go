package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const keyName = "pais-ssh_ed25519"

// The markers of an environment's block in ~/.ssh/config.
func (e env) blockBegin() string {
	return "# --- " + e.name + ": written by pais-ssh setup, edits here are replaced ---"
}
func (e env) blockEnd() string { return "# --- " + e.name + " end ---" }

// setup signs in if needed, makes the key and writes the managed block in ~/.ssh/config.
func setup(e env, noBrowser bool) error {
	tok, c, err := token(e)
	if err != nil {
		if _, err := login(e, noBrowser); err != nil {
			return err
		}
		if tok, c, err = token(e); err != nil {
			return err
		}
	}
	ws, err := workspaces(e, tok, c.Email, "")
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := ensureKey(filepath.Join(dir, keyName), c.Email); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cfg := filepath.Join(dir, "config")
	user := workspaceUser(tok, ws)
	if user == "" {
		user = c.username()
		fmt.Printf("No running Workspace told its login name; using %q from your sign-in. Start one and run setup again if ssh says \"Permission denied\".\n", user)
	}
	if err := writeBlock(e, cfg, block(e, exe, user, hostNames(e, ws))); err != nil {
		return err
	}

	fmt.Printf("Wrote the %s block in %s.\n", e.name, cfg)
	printWorkspaces(e, ws)
	return nil
}

var loginName = regexp.MustCompile(`^[a-z_][a-z0-9._-]*$`)

// workspaceUser asks your running Workspaces for their login name (`id -un`), which sshd requires and which the
// start-up serves at <SSH URL>/user through the same owner-only sidecar. The sign-in token can't give it: on some
// tenants its only name is the email. "" when no Workspace answers.
func workspaceUser(tok string, ws []workload) string {
	client := &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	for _, w := range ws {
		if w.Phase != "Running" {
			continue
		}
		req, err := http.NewRequest(http.MethodGet, strings.TrimSuffix(w.sshURL(), "/")+"/user", nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		resp.Body.Close()
		if name := strings.TrimSpace(string(b)); resp.StatusCode == http.StatusOK && loginName.MatchString(name) {
			return name
		}
	}
	return ""
}

// ensureKey makes the key the first time. It never leaves this machine: sshd in the Workspace accepts any key from
// its owner, whom the sidecar has already checked; ssh just needs one to offer.
func ensureKey(path, comment string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, "pais-ssh "+comment)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " pais-ssh " + comment + "\n"
	return os.WriteFile(path+".pub", []byte(line), 0o644)
}

// hostNames gives <env>.<workspace>, or <env>.<workspace>.<project> for names in more than one project.
func hostNames(e env, ws []workload) []string {
	count := map[string]int{}
	for _, w := range ws {
		count[w.Name]++
	}
	var names []string
	for _, w := range ws {
		n := e.name + "." + w.Name
		if count[w.Name] > 1 {
			n += "." + w.ProjectName
		}
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// block is the managed part of ~/.ssh/config. The wildcard entry reaches any Workspace, including ones made later;
// the line naming each current Workspace is only there so VS Code's Remote Explorer can list them.
func block(e env, exe, user string, hosts []string) string {
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"`
	}
	var b strings.Builder
	b.WriteString(e.blockBegin() + "\n")
	if len(hosts) > 0 {
		b.WriteString("Host " + strings.Join(hosts, " ") + "\n")
	}
	b.WriteString("Host " + e.name + ".*\n")
	b.WriteString("  User " + user + "\n")
	b.WriteString("  ProxyCommand " + exe + " proxy %n\n")
	b.WriteString("  IdentityFile ~/.ssh/" + keyName + "\n")
	b.WriteString("  IdentitiesOnly yes\n")
	// The host key is new each time the Workspace starts. TLS to the cluster already shows which server this is.
	b.WriteString("  StrictHostKeyChecking no\n")
	b.WriteString("  UserKnownHostsFile /dev/null\n")
	b.WriteString("  LogLevel ERROR\n")
	// The gateways close connections idle for 60 s.
	b.WriteString("  ServerAliveInterval 30\n")
	// With ControlPersist set (often in a user's "Host *"), ssh sends the ProxyCommand's stderr to /dev/null, which
	// hides pais-ssh's messages such as "not signed in". Multiplexing within an open session still works.
	b.WriteString("  ControlPersist no\n")
	b.WriteString(e.blockEnd() + "\n")
	return b.String()
}

// writeBlock puts the block at the top of the file, where ssh reads it before any other "Host *" settings, replacing
// any earlier block for this environment (including one written by config-ssh). Other environments' blocks stay.
func writeBlock(e env, path, blk string) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var rest []string
	skip := false
	for _, line := range strings.SplitAfter(string(old), "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "# --- "+e.name+": ") {
			skip = true
		}
		if !skip && line != "" {
			rest = append(rest, line)
		}
		if trimmed == e.blockEnd() {
			skip = false
		}
	}
	return os.WriteFile(path, []byte(blk+"\n"+strings.TrimLeft(strings.Join(rest, ""), "\r\n")), 0o600)
}

func list(e env) error {
	tok, c, err := token(e)
	if err != nil {
		return err
	}
	ws, err := workspaces(e, tok, c.Email, "")
	if err != nil {
		return err
	}
	printWorkspaces(e, ws)
	return nil
}

func printWorkspaces(e env, ws []workload) {
	if len(ws) == 0 {
		fmt.Println("None of your Workspaces has SSH. Make one from a template with an SSH connection.")
		return
	}
	names := hostNames(e, ws)
	byName := map[string]workload{}
	for _, w := range ws {
		byName[e.name+"."+w.Name] = w
		byName[e.name+"."+w.Name+"."+w.ProjectName] = w
	}
	for _, n := range names {
		w := byName[n]
		fmt.Printf("  ssh %-36s project %-20s %s\n", n, w.ProjectName, w.Phase)
	}
}
