# pais-ssh

One program that gives plain OpenSSH (`ssh`, `scp`, `sftp`, `rsync`, VS Code Remote-SSH) to your own Run:ai Workspaces.
Besides it, your computer needs only OpenSSH, which comes with Linux, macOS and Windows 10 and later.
No administrator rights.

**Experimental.** From the experiment `ws-ssh` (branch `experiment/ws-ssh` of drai-inn/ai-gpu-deployment). Works only
with Workspaces whose template has an SSH Connect URL:

| Environment (`--env`, host prefix) | Run:ai tenant | Template |
|---|---|---|
| `drai-test` | drai-test, ai-gpu-test | `exp-ws-ssh-shell` in project `exp-ws-ssh` (ai-gpu-deployment `experiment/ws-ssh`) |
| `pais-dev` (default) | auckland, ASI dev; reachable only on the ASI network | `ubuntu-shell-testing` (pais-deploy-dev, `services/runai-control-plane/templates/auckland/`) |

Run:ai copies the template into a Workspace when it is created, so a template change (such as a new SSH Connect URL)
reaches only Workspaces created after it. Stopping and starting a Workspace keeps its old spec: make a new one.

```
ssh drai-test.cs-ssh3
  -> ProxyCommand: pais-ssh proxy drai-test.cs-ssh3
       sign-in token (refreshed from the stored sign-in)
       Run:ai API: find the Workspace's SSH URL; resume it if stopped
       WebSocket to the SSH URL, Authorization: Bearer <token>
  -> hub-gateway -> Run:ai route -> oauth2-proxy sidecar (owner only)
  -> websocat on localhost:4192 -> sshd -i (as the owner, any key, owner's username only)
```

## Install

Download the archive for your system from [Releases](https://github.com/drai-inn/pais-ssh/releases/latest)
(`pais-ssh_<os>_<arch>`: `linux`, `darwin` for macOS, `windows`; `amd64`, or `arm64` for Apple silicon), check it
against `checksums.txt`, and put `pais-ssh` in a folder on your `PATH`, where it will stay: `setup` writes its full path
into `~/.ssh/config`. With the GitHub CLI, for example on an Apple silicon Mac:

```sh
gh release download -R drai-inn/pais-ssh -p 'pais-ssh_darwin_arm64.tar.gz' -p checksums.txt
shasum -a 256 --ignore-missing -c checksums.txt
tar -xzf pais-ssh_darwin_arm64.tar.gz pais-ssh && mkdir -p ~/.local/bin && mv pais-ssh ~/.local/bin/
```

The binaries are not signed. macOS blocks a binary downloaded with a browser until you run
`xattr -d com.apple.quarantine ~/.local/bin/pais-ssh`; one fetched with `gh` or `curl` isn't marked. Windows may show a
SmartScreen warning.

`pais-ssh version` shows which release you have.

### Build from source

Go 1.26 or later: `go build -o pais-ssh .`

### Release

Push a tag `v<major>.<minor>.<patch>`. The `release` workflow builds Linux, macOS and Windows for amd64 and arm64 with
GoReleaser (`.goreleaser.yaml`) and publishes them, with `checksums.txt`, as a GitHub Release. The `ci` workflow builds
the same targets on every push to `main` and every pull request.

## Use

```sh
pais-ssh setup                   # pais-dev: sign in (browser), make ~/.ssh/pais-ssh_ed25519, write its ~/.ssh/config block
ssh pais-dev.<workspace>
pais-ssh list                    # your Workspaces with SSH there
pais-ssh setup --env drai-test   # the same for drai-test
ssh drai-test.<workspace>
```

- Each environment has its own sign-in and its own block in `~/.ssh/config`; the key is shared.
- `setup` writes one wildcard entry per environment, for example `Host drai-test.*`, so new Workspaces need no new
  set-up. It also names your
  current Workspaces on a `Host` line so that VS Code's Remote Explorer lists them; run `setup` again to refresh that.
  It replaces only its own block, at the top of the file, including a block written earlier by the experiment's
  `client/config-ssh`.
- A name in more than one of your projects: `ssh <env>.<workspace>.<project>`.
- A stopped Workspace is started and waited for (up to 5 minutes), and left running.
- The sign-in is stored in the user config directory (`~/.config/pais-ssh/<env>.json` on Linux) and refreshed when
  needed, until the Keycloak session ends (about a day on drai-test). Then ssh fails with "run `pais-ssh login --env
  <env>`".
- Signing in on a machine without a browser: `pais-ssh login --no-browser` prints the URL. The browser must reach
  `http://localhost:8000` on that machine (or 18000 when 8000 is taken):
  `ssh -L 8000:127.0.0.1:8000 -L 18000:127.0.0.1:18000 <machine>`.
- `pais-ssh logout --env <env>` deletes that environment's stored sign-in. The key and the `~/.ssh/config` block stay.

**drai-test Workspaces** started before 2026-11-17 check `~/.ssh/authorized_keys`, so pais-ssh's key is refused
there ("Permission denied (publickey)").

## How it decides

- Sign-in: authorisation code flow with PKCE against the tenant's realm in Run:ai's Keycloak, with Run:ai's public
  client `runai-cli` (the one kubelogin uses). Its ID token is accepted by the Run:ai API and by the sidecar. Keycloak allows
  `http://localhost:<any port>` as the redirect for this client, not `http://127.0.0.1`.
- Login name: `setup` asks one of your running Workspaces (`<SSH URL>/user`, which the start-up fills with `id -un`),
  because sshd accepts only that name and the token may not carry it (on pais-dev its only name is the email). With no
  running Workspace, or one whose template doesn't serve it, the token's `preferred_username` without the domain.
- Workspaces: `GET /api/v1/workloads?filterBy=type==Workspace,submittedBy==<email>[,name==<workspace>]`, keeping those
  with a connection named `SSH...`.
- The key never leaves the computer. ssh has to offer one; sshd in the Workspace accepts any for the owner, because the
  sidecar has already checked the token.
- Host keys are not recorded (`StrictHostKeyChecking no`): the Workspace makes a new one each start, and TLS to the
  cluster already identifies the server.
- `ControlPersist no`: with `ControlPersist` set (for example in your own `Host *`), ssh sends the ProxyCommand's
  messages to `/dev/null`, so a failure such as an expired sign-in shows nothing and ssh just exits 255. Sessions
  still share one connection while the first is open (`ControlMaster`).

## Tested

2026-11-17, Linux, built from source, against `cs-ssh3` and `cs-ssh4` in `exp-ws-ssh` (sshd settings applied by hand on `cs-ssh3`):

| | Result |
|---|---|
| Token refresh from an expired ID token | works |
| `setup` twice | same block, rest of the file kept |
| `ssh drai-test.cs-ssh3`, and `.exp-ws-ssh` suffix | works, 0.41 s per connection |
| rsync 50 MB | up 2.8 to 8.6 s, down 1.1 s, files identical; websocat varied as much (1.7 to 8.9 s up) at the same time |
| Stopped Workspace | resumed, sshd answering after 15.5 s |
| Unknown Workspace, wrong project, Workspace without SSH | clear error, ssh exits 255 |
| Sign-in request (redirect to localhost:18000, PKCE S256) | Keycloak shows its login form |

Not tested: the browser sign-in to the end, macOS, Windows, VS Code Remote-SSH, another person's token refused on
someone else's Workspace.
