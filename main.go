// pais-ssh gives plain OpenSSH (ssh, scp, rsync, VS Code Remote-SSH) to your own Run:ai Workspaces.
//
// The SSH connection runs over a WebSocket to the Workspace's SSH URL, with your Run:ai sign-in as a bearer token.
// Run:ai's owner-only oauth2-proxy sidecar checks the token; sshd in the Workspace accepts any key for its owner.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

// env is one Run:ai tenant. Its name is also the ssh host prefix, <name>.<workspace>, so several environments can
// share one ~/.ssh/config.
type env struct {
	name    string
	issuer  string // the tenant's realm in Run:ai's Keycloak
	apiBase string // the tenant's Run:ai API
}

var envs = []env{
	{
		name:    "drai-test",
		issuer:  "https://runai-mt.test.drai.auckland.ac.nz/auth/realms/drai-test",
		apiBase: "https://drai-test.runai-mt.test.drai.auckland.ac.nz",
	},
	{
		// The PAIS dev environment, tenant auckland (VPN only).
		name:    "pais-dev",
		issuer:  "https://runai.dev.pais.auckland.ac.nz/auth/realms/auckland",
		apiBase: "https://auckland.runai.dev.pais.auckland.ac.nz",
	},
}

// clientID is Run:ai's public CLI client, present in every tenant realm.
const clientID = "runai-cli"

const defaultEnv = "pais-dev"

func findEnv(name string) (env, error) {
	var names []string
	for _, e := range envs {
		if e.name == name {
			return e, nil
		}
		names = append(names, e.name)
	}
	return env{}, fmt.Errorf("no environment %q (one of: %s)", name, strings.Join(names, ", "))
}

// version is set at release build time (-ldflags -X main.version=...).
var version = "dev"

const usage = `pais-ssh: ssh, scp, rsync and VS Code Remote-SSH to your Run:ai Workspaces.

  pais-ssh setup  [--env E] [--no-browser]   sign in, make a key and write ~/.ssh/config
                                             (run again to refresh the host list VS Code shows)
  pais-ssh login  [--env E] [--no-browser]   sign in again
  pais-ssh list   [--env E]                  your Workspaces with SSH
  pais-ssh logout [--env E]                  forget the stored sign-in
  pais-ssh version                           this build's version

--env is pais-dev (the default) or drai-test. Then:
  ssh <env>.<workspace>
  ssh <env>.<workspace>.<project>   when the name is in more than one project

--no-browser prints the sign-in URL instead of opening a browser. The browser must reach
http://localhost:8000 on this machine, or 18000 when 8000 is in use (from elsewhere:
ssh -L 8000:127.0.0.1:8000 -L 18000:127.0.0.1:18000 <this machine>).
`

func main() {
	log.SetFlags(0)
	log.SetPrefix("pais-ssh: ")
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	envName := fs.String("env", defaultEnv, "environment: pais-dev or drai-test")
	noBrowser := fs.Bool("no-browser", false, "print the sign-in URL instead of opening a browser")
	fs.Parse(args)

	if cmd == "version" {
		fmt.Println(version)
		return
	}
	if cmd == "proxy" {
		if err := proxy(fs.Args()); err != nil {
			log.Fatal(err)
		}
		return
	}

	e, err := findEnv(*envName)
	if err != nil {
		log.Fatal(err)
	}
	switch cmd {
	case "setup":
		err = setup(e, *noBrowser)
	case "login":
		_, err = login(e, *noBrowser)
	case "list":
		err = list(e)
	case "logout":
		err = logout(e)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}
