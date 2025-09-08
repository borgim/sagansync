### Cursor Rules — SaganSync

These rules guide all contributions, planning, and implementation for SaganSync inside Cursor.

### Scope and philosophy
- **Simplicity first**: One local config file only: `.sagansync/config.json` (gitignored).
- **Framework agnostic**: The only runtime contract is a Dockerfile/Containerfile and an exposed internal port.
- **Opinionated but overridable**: Default stack is Podman (rootless) + Caddy (systemd) + SSH. Users can override image/port/env in config.
- **Minimal footprint**: Do not add repo files (Compose, templates, units) unless explicitly requested.

### Config contract
- Required keys in `.sagansync/config.json`:
  - **host**: `user@ip`
  - **remotePath**: absolute path on VPS
  - **internalPort**: number (the app’s port in the container)
  - **domain**: optional for MVP
  - **ssh**: `{ port?: number, identityFile?: string }` optional
- Validate and fail fast if required fields are missing or malformed.

Example minimal config:
```json
{
  "host": "root@10.0.0.1",
  "remotePath": "/srv/myapp",
  "internalPort": 3000,
  "domain": "api.example.com",
  "ssh": { "port": 22, "identityFile": "~/.ssh/id_ed25519" }
}
```

### Infrastructure policy
- **Containers**: Podman (rootless) only.
- **Proxy/TLS**: Caddy as a host systemd service (not containerized) for simpler TLS on :80/:443.
- **Transport**: SSH for all remote operations; commands should be non-interactive by default.

### Security and networking
- App containers bind to `127.0.0.1:NNNNN` only; public exposure is through Caddy.
- Open firewall for 22/80/443, nothing else.
- Secrets are managed on VPS (e.g., `.env` in `remotePath`) with `chmod 600`; injected via `--env-file` or env vars at run.
- Prefer strict host key checking ON; allow DX override flag when needed.

### Deployment flow (MVP)
1) `rsync` project to `remotePath`.
2) `podman build` image from the project’s Dockerfile.
3) Choose a free loopback port; `podman run -p 127.0.0.1:port:internalPort` with a predictable container name.
4) Update Caddy upstream to the chosen port; validate config; reload Caddy.
5) Print final URL and container status.

### Commands and flags
- `sagansync init`: create `.sagansync/config.json` via prompts.
- `sagansync provision`: install Podman + Caddy; create `remotePath`; configure and enable Caddy; open firewall.
- `sagansync deploy [--blue-green] [--env-file <path>] [--tag <tag>]`: build, run on a new port, switch Caddy.
- `sagansync secrets set <KEY>=<VAL> | list`: manage `.env` on VPS (redact values when listing).
- `sagansync logs`: stream current app container logs.
- `sagansync status`: versions, domain, upstream port, last deploys.
- `sagansync doctor`: diagnostics for SSH/Podman/Caddy/DNS/HTTP.
- Provide `--dry-run` and `--verbose` where sensible.

### Idempotency and DX
- All commands must be idempotent; re-running should not break a working setup.
- Detect and skip already-installed components; print clear, actionable messages.
- Colorized, concise output; show next steps on failures.

### OS support
- Target Ubuntu/Debian first; detect unsupported systems and fail clearly with guidance.

### Rollouts (post-MVP)
- Blue/green: start new container on new port; health-check; switch Caddy; stop old; retain last N for rollback.

### Do not rules
- Do not expose containers publicly; only via Caddy.
- Do not persist secrets locally beyond `.sagansync/config.json`.
- Do not create additional files in the repo unless explicitly requested by the user.

### Change management
- Propose schema changes to `.sagansync/config.json` explicitly and keep them backwards compatible when possible.
- Keep CLI flags stable; mark breaking changes and provide migration notes.


