// pais-ssh gives plain OpenSSH (ssh, scp, rsync, VS Code Remote-SSH) to your own Run:ai Workspaces on drai-test.
//
// The SSH connection runs over a WebSocket to the Workspace's SSH URL, with your drai sign-in as a bearer token.
// Run:ai's owner-only oauth2-proxy sidecar checks the token; sshd in the Workspace accepts any key for its owner.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

const (
	issuer   = "https://runai-mt.test.drai.auckland.ac.nz/auth/realms/drai-test"
	clientID = "runai-cli"
	apiBase  = "https://drai-test.runai-mt.test.drai.auckland.ac.nz"
	// prefix names the environment in ssh host aliases, so test and prod entries can share one ~/.ssh/config.
	prefix = "drai-test"
)

// version is set at release build time (-ldflags -X main.version=...).
var version = "dev"

const usage = `pais-ssh: ssh, scp, rsync and VS Code Remote-SSH to your Run:ai Workspaces.

  pais-ssh setup [--no-browser]   sign in, make a key and write ~/.ssh/config
                                  (run again to refresh the host list VS Code shows)
  pais-ssh login [--no-browser]   sign in again
  pais-ssh list                   your Workspaces with SSH
  pais-ssh logout                 forget the stored sign-in
  pais-ssh version                this build's version

Then: ssh ` + prefix + `.<workspace>
      ssh ` + prefix + `.<workspace>.<project>   when the name is in more than one project

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
	noBrowser := fs.Bool("no-browser", false, "print the sign-in URL instead of opening a browser")
	fs.Parse(args)

	var err error
	switch cmd {
	case "setup":
		err = setup(*noBrowser)
	case "login":
		_, err = login(*noBrowser)
	case "list":
		err = list()
	case "logout":
		err = logout()
	case "proxy":
		err = proxy(fs.Args())
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}
