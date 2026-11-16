package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"
)

const keyName = "pais-ssh_ed25519"

var (
	blockBegin = "# --- " + prefix + ": written by pais-ssh setup, edits here are replaced ---"
	blockEnd   = "# --- " + prefix + " end ---"
)

// setup signs in if needed, makes the key and writes the managed block in ~/.ssh/config.
func setup(noBrowser bool) error {
	tok, c, err := token()
	if err != nil {
		if _, err := login(noBrowser); err != nil {
			return err
		}
		if tok, c, err = token(); err != nil {
			return err
		}
	}
	ws, err := workspaces(tok, c.Email, "")
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
	if err := writeBlock(cfg, block(exe, c.username(), hostNames(ws))); err != nil {
		return err
	}

	fmt.Printf("Wrote %s.\n", cfg)
	printWorkspaces(ws)
	return nil
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

// hostNames gives prefix.<workspace>, or prefix.<workspace>.<project> for names in more than one project.
func hostNames(ws []workload) []string {
	count := map[string]int{}
	for _, w := range ws {
		count[w.Name]++
	}
	var names []string
	for _, w := range ws {
		n := prefix + "." + w.Name
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
func block(exe, user string, hosts []string) string {
	if strings.ContainsAny(exe, " \t") {
		exe = `"` + exe + `"`
	}
	var b strings.Builder
	b.WriteString(blockBegin + "\n")
	if len(hosts) > 0 {
		b.WriteString("Host " + strings.Join(hosts, " ") + "\n")
	}
	b.WriteString("Host " + prefix + ".*\n")
	b.WriteString("  User " + user + "\n")
	b.WriteString("  ProxyCommand " + exe + " proxy %n\n")
	b.WriteString("  IdentityFile ~/.ssh/" + keyName + "\n")
	b.WriteString("  IdentitiesOnly yes\n")
	// The host key is new each time the Workspace starts. TLS to the cluster already shows which server this is.
	b.WriteString("  StrictHostKeyChecking no\n")
	b.WriteString("  UserKnownHostsFile /dev/null\n")
	b.WriteString("  LogLevel ERROR\n")
	// hub-gateway closes connections idle for 60 s.
	b.WriteString("  ServerAliveInterval 30\n")
	b.WriteString(blockEnd + "\n")
	return b.String()
}

// writeBlock puts the block at the top of the file, where ssh reads it before any other "Host *" settings, replacing
// any earlier block for this prefix (including one written by config-ssh).
func writeBlock(path, blk string) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var rest []string
	skip := false
	for _, line := range strings.SplitAfter(string(old), "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "# --- "+prefix+": ") {
			skip = true
		}
		if !skip && line != "" {
			rest = append(rest, line)
		}
		if trimmed == blockEnd {
			skip = false
		}
	}
	return os.WriteFile(path, []byte(blk+"\n"+strings.TrimLeft(strings.Join(rest, ""), "\r\n")), 0o600)
}

func list() error {
	tok, c, err := token()
	if err != nil {
		return err
	}
	ws, err := workspaces(tok, c.Email, "")
	if err != nil {
		return err
	}
	printWorkspaces(ws)
	return nil
}

func printWorkspaces(ws []workload) {
	if len(ws) == 0 {
		fmt.Println("None of your Workspaces has SSH. Make one from a template with an SSH connection.")
		return
	}
	names := hostNames(ws)
	byName := map[string]workload{}
	for _, w := range ws {
		byName[prefix+"."+w.Name] = w
		byName[prefix+"."+w.Name+"."+w.ProjectName] = w
	}
	for _, n := range names {
		w := byName[n]
		fmt.Printf("  ssh %-36s project %-20s %s\n", n, w.ProjectName, w.Phase)
	}
}
