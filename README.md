<p align="center">
  <img src="./sagansync.png" width="80" alt="" />
  <h1 align="center">SaganSync</h1>
</p>

**Every git branch gets its own HTTPS URL on your own VPS.** Deploy with zero downtime, preview branches on their own subdomains, and live-edit a branch on the server while you code. No CI pipeline, no Kubernetes, no platform account: one small agent on the server and a CLI on your machine.

```console
$ git switch -c feat-login
$ sagansync deploy
▸ Building image
▸ Waiting for the health check
▸ Getting the TLS certificate
✔ Live at https://feat-login-myapp.example.com
```

- **Zero-downtime deploys.** The new release must pass a health check before traffic moves to it. If it fails, the old one keeps serving and you see its last log lines.
- **A URL per branch.** `main` goes to your domain; every other branch gets `<branch>-<project>.<your-preview-domain>`, each with its own certificate.
- **Live dev mode.** `sagansync dev` runs the branch in dev mode on the server and syncs every file you save, so you can share a URL with a client while you work.
- **Secure by default.** After a one-time setup nothing runs as root, containers are rootless, and the deploy key can only run the agent's commands: no shell, no tunnels.

> Inspired by indie devs and Carl Sagan: clarity, connection and exploration.

## Requirements

- **Server:** Ubuntu 24.04+ or Debian 12+ (x86_64 or arm64), with root or passwordless sudo for the one-time setup, and ports 80 and 443 free.
- **Your machine:** macOS or Linux with Node.js 22.12+ and OpenSSH. Windows is not supported yet (its OpenSSH lacks connection sharing).
- **Your project:** a `Dockerfile` (or `Containerfile`; if both exist, `Containerfile` wins, as in Podman) that starts an HTTP server.

## Quick start

```sh
npm install -g sagansync

cd my-project
sagansync init                                  # server address, project name, domains
sagansync provision --admin root@203.0.113.7    # installs the agent (once per server)
sagansync deploy
```

`init` writes `.sagansync/config.json`, which holds no secrets and can be committed, and creates a deploy key in `~/.config/sagansync/keys/`.

## DNS

Point your production domain at the server, and add **one wildcard record** for branch previews:

| Record | Points to |
| --- | --- |
| `A  api.example.com` | your server's IP |
| `A  *.example.com` | your server's IP (covers the previews of every project) |

With `"previewDomain": "example.com"`, branch `feat-login` of project `myapp` is served at `feat-login-myapp.example.com`. Explicit records such as `www` or `mail` keep working.

- **Registro.br** may not accept `*` records. Keep the domain registered there and move its nameservers to Cloudflare (free), creating the wildcard as **DNS only** (grey cloud). Cloudflare's proxy (orange cloud) is not supported.
- Every hostname gets its own Let's Encrypt certificate, and certificates are public in Certificate Transparency logs (e.g. crt.sh). Avoid branch names you don't want published.
- Let's Encrypt allows 50 certificates per registered domain per week, which is plenty for branches.

`sagansync deploy` warns when a hostname does not point at the server yet.

## Commands

| Command | What it does |
| --- | --- |
| `sagansync init` | Configure the project and create its deploy key. |
| `sagansync provision [--admin user@host] [--admin-key path]` | Install or repair the agent. `--upgrade` only replaces it, `--acme-email` sets your Let's Encrypt contact, `--remove-caddy` removes the Caddy an older SaganSync installed. |
| `sagansync deploy [-w workspace] [-v]` | Build and release the current branch with zero downtime. |
| `sagansync dev [-c "npm run dev"] [--build]` | Run the branch in dev mode and sync local edits. Refuses `production` unless `--force`. Stops if you switch branches. |
| `sagansync list [--all]` | Workspaces of this project (or of the whole server). |
| `sagansync logs [-n 100] [-f]` | Container logs. |
| `sagansync env set KEY=VALUE … \| --file .env.production` | Set variables for the next deploy. Values travel over SSH stdin, never on a command line. |
| `sagansync env unset KEY … / env list` | Remove or list variables (values are never shown). |
| `sagansync remove [-y]` | Delete a workspace: container, releases, files, variables. |

Workspaces come from the git branch: `main`/`master` → `production`, `develop`/`dev` → `staging`, anything else → the branch name. Use `-w` to choose one.

Files in `.gitignore` and `.dockerignore` are not uploaded, and `.git`, `node_modules`, `.sagansync` and every `.env*` file never are.

## Configuration

`.sagansync/config.json`:

```jsonc
{
  "host": "203.0.113.7",          // server hostname or IP
  "sshPort": 22,
  "project": "myapp",
  "internalPort": 3000,           // port your app listens on inside the container
  "domain": "api.example.com",    // optional: production
  "previewDomain": "example.com", // optional: branches at <branch>-<project>.example.com
  "healthPath": "/health",        // optional: HTTP health check (default: TCP)
  "healthTimeout": 60             // optional, seconds
}
```

## How it works

```
your machine                           server
sagansync ──ssh (deploy key)──▶ sshd ─▶ sagand gateway ─▶ sagand daemon ─▶ rootless Podman
                                        (forced command)   ├ HTTPS proxy + Let's Encrypt
                                                           └ zero-downtime swaps, state
```

`provision` creates an unprivileged `sagan` user, installs Podman and the `sagand` agent as a hardened systemd service, and authorizes the deploy key **only** for `sagand gateway`. The daemon binds ports 80/443 through `CAP_NET_BIND_SERVICE` alone, talks to Podman through its API, and never runs a shell. After a reboot it brings every workspace back on its own.

The design is documented in [`docs/superpowers/specs`](docs/superpowers/specs/2026-09-24-sagansync-v0.1-agent-design.md).

## Troubleshooting

- **`REMOTE HOST IDENTIFICATION HAS CHANGED`**: if you reinstalled the server, delete its line from `~/.config/sagansync/known_hosts`.
- **`The admin account needs root or passwordless sudo`**: use `--admin root@<host>`, or allow passwordless sudo for that user.
- **`Ports 80/443 are already in use`**: stop the web server that holds them (nginx, apache…), then run `provision` again.
- **A deploy fails its health check**: the last lines of the container's log are printed; `sagansync logs` shows more. Your previous release is still live.

## Development

```
agent/   Go: the sagand daemon, gateway and client        cd agent && go test -race ./...
cli/     TypeScript: the sagansync CLI                      cd cli && npm test
test/e2e End-to-end test on a Lima VM with real Podman     test/e2e/run.sh
```

The end-to-end test needs [Lima](https://lima-vm.io), Go and Node. It creates an Ubuntu 24.04 VM, provisions it with the real CLI, uses [Pebble](https://github.com/letsencrypt/pebble) as the ACME server, and checks deploys, zero downtime, rollback, dev mode, the deploy key's restrictions, upgrade and reboot.

Releases are cut by pushing a `v*` tag matching `cli/package.json`'s version: GitHub Actions publishes the agent binaries to GitHub Releases and then the CLI to npm.

## License

MIT
