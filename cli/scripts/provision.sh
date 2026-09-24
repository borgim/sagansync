#!/bin/bash
# provision.sh installs sagand on a Debian/Ubuntu server. `sagansync provision`
# uploads it with the files it needs and runs it as root:
#
#   provision.sh <bundle-dir> [--upgrade] [--remove-caddy]
#
# The bundle holds: sagand (the agent binary), deploy.pub (the project's deploy
# key), config.json (daemon settings) and optionally acme-root-ca.pem.
# Running it again is safe; --upgrade skips package installation and keeps the
# existing daemon settings.
set -euo pipefail

BUNDLE=$1
shift
UPGRADE=0
REMOVE_CADDY=0
for arg in "$@"; do
  case $arg in
    --upgrade) UPGRADE=1 ;;
    --remove-caddy) REMOVE_CADDY=1 ;;
    *) echo "provision.sh: unknown option $arg" >&2; exit 2 ;;
  esac
done

step() { echo "▸ $*"; }
fail() { echo "✖ $*" >&2; exit 1; }
# Files under /home/sagan and /var/lib/sagand belong to sagan, so sagan could
# replace them with symlinks (e.g. to /etc/shadow). They are always written as
# sagan, never as root.
as_sagan() { runuser -u sagan -- "$@"; }
# write_as_sagan <source> <destination>
write_as_sagan() {
  as_sagan sh -c 'umask 077; if [ -L "$1" ]; then echo "✖ $1 is a symlink; refusing to write through it. Remove it and run provision again." >&2; exit 1; fi; cat > "$1"' sh "$2" <"$1"
}

[ "$(id -u)" -eq 0 ] || fail "provision.sh must run as root"

# --- System -------------------------------------------------------------------
# shellcheck source=/dev/null # only exists on the server
. /etc/os-release
case "$ID" in
  ubuntu) dpkg --compare-versions "$VERSION_ID" ge 24.04 || fail "Ubuntu $VERSION_ID is too old: sagand needs Ubuntu 24.04 or newer (for Podman 4.3+)." ;;
  debian) dpkg --compare-versions "$VERSION_ID" ge 12 || fail "Debian $VERSION_ID is too old: sagand needs Debian 12 or newer (for Podman 4.3+)." ;;
  *) fail "$PRETTY_NAME is not supported: sagand needs Ubuntu 24.04+ or Debian 12+." ;;
esac

# --- Ports 80 and 443 -----------------------------------------------------------
busy=$(ss -Hltnp '( sport = :80 or sport = :443 )' | grep -v '"sagand"' || true)
if [ -n "$busy" ]; then
  if echo "$busy" | grep -q '"caddy"' && [ -d /etc/caddy/conf.d ]; then
    [ "$REMOVE_CADDY" -eq 1 ] || fail "Caddy from an older SaganSync is using ports 80/443. Run again with --remove-caddy to remove it."
    step "Removing Caddy"
    systemctl disable --now caddy >/dev/null 2>&1 || true
    DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq -o DPkg::Lock::Timeout=300 caddy >/dev/null
    backup="/etc/caddy.removed-by-sagansync.$(date +%Y%m%d%H%M%S)"
    mv /etc/caddy "$backup"
    echo "  Caddy's configuration was kept in $backup"
  else
    echo "$busy" >&2
    fail "Ports 80/443 are already in use (see above). sagand needs them for HTTPS: stop that service and run again."
  fi
fi

# --- Packages --------------------------------------------------------------------
if [ "$UPGRADE" -eq 0 ]; then
  step "Installing Podman"
  export DEBIAN_FRONTEND=noninteractive
  # A fresh VPS often runs unattended-upgrades on first boot: wait for its lock.
  apt-get update -qq -o DPkg::Lock::Timeout=300
  apt-get install -y -qq -o DPkg::Lock::Timeout=300 podman uidmap passt slirp4netns dbus-user-session >/dev/null
fi
podman_version=$(podman version --format '{{.Client.Version}}')
dpkg --compare-versions "$podman_version" ge 4.3 || fail "Podman $podman_version is too old: sagand needs 4.3 or newer."

# --- The sagan user ----------------------------------------------------------------
# Only an account this script created is reused: its home is /home/sagan and
# /var/lib/sagand exists (created right after the account).
if id sagan >/dev/null 2>&1; then
  if [ "$(getent passwd sagan | cut -d: -f6)" != /home/sagan ] || [ ! -d /var/lib/sagand ]; then
    fail "A user named sagan already exists and was not created by SaganSync. Remove or rename it, then run provision again."
  fi
else
  getent group sagan >/dev/null && fail "A group named sagan already exists and was not created by SaganSync. Remove or rename it, then run provision again."
  step "Creating the sagan user"
  useradd --system --user-group --create-home --home-dir /home/sagan --shell /bin/sh sagan
  install -d -o sagan -g sagan -m 0700 /var/lib/sagand
fi
passwd -l sagan >/dev/null
# useradd --system does not allocate subordinate ids, which rootless Podman needs.
for f in /etc/subuid /etc/subgid; do
  touch "$f"
  if ! grep -q '^sagan:' "$f"; then
    start=$(awk -F: 'BEGIN { m = 100000 } { e = $2 + $3; if (e > m) m = e } END { print m }' "$f")
    echo "sagan:$start:65536" >> "$f"
  fi
done
uid=$(id -u sagan)

step "Starting rootless Podman for sagan"
loginctl enable-linger sagan
systemctl start "user@$uid.service"
systemctl --user --machine=sagan@ enable --now podman.socket >/dev/null 2>&1

# --- Files ---------------------------------------------------------------------------
step "Installing sagand"
install -d -o sagan -g sagan -m 0700 /srv/sagan /var/lib/sagand
install -o root -g root -m 0755 "$BUNDLE/sagand" /usr/local/bin/sagand.new
mv /usr/local/bin/sagand.new /usr/local/bin/sagand
if [ "$UPGRADE" -eq 0 ] || [ ! -f /var/lib/sagand/config.json ]; then
  write_as_sagan "$BUNDLE/config.json" /var/lib/sagand/config.json
fi
if [ -f "$BUNDLE/acme-root-ca.pem" ]; then
  write_as_sagan "$BUNDLE/acme-root-ca.pem" /var/lib/sagand/acme-root-ca.pem
fi

# --- Deploy key: it may only run the sagand gateway --------------------------------
ssh-keygen -l -f "$BUNDLE/deploy.pub" >/dev/null 2>&1 || fail "deploy.pub is not a valid SSH public key."
key=$(head -n 1 "$BUNDLE/deploy.pub")
line="command=\"/usr/local/bin/sagand gateway\",restrict $key"
as_sagan sh -c '
  umask 077
  cd /home/sagan || exit 1
  for p in .ssh .ssh/authorized_keys; do
    if [ -L "$p" ]; then
      echo "✖ /home/sagan/$p is a symlink; refusing to write through it. Remove it and run provision again." >&2
      exit 1
    fi
  done
  mkdir -p .ssh && touch .ssh/authorized_keys || exit 1
  grep -qxF "$1" .ssh/authorized_keys || printf "%s\n" "$1" >>.ssh/authorized_keys || exit 1
  chmod 700 .ssh && chmod 600 .ssh/authorized_keys
' sh "$line"

# --- Service -------------------------------------------------------------------------
cat > /etc/systemd/system/sagand.service <<EOF
[Unit]
Description=SaganSync agent
After=network-online.target user@$uid.service
Wants=network-online.target
Requires=user@$uid.service

[Service]
User=sagan
Group=sagan
ExecStart=/usr/local/bin/sagand daemon
RuntimeDirectory=sagand
RuntimeDirectoryMode=0700
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=tmpfs
BindPaths=/run/user/$uid/podman
PrivateTmp=yes
ReadWritePaths=/srv/sagan /var/lib/sagand
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable sagand >/dev/null 2>&1
systemctl restart sagand

# Not "ufw status | grep -q": with pipefail, a long rule list can make that
# pipeline fail after grep has already matched.
if command -v ufw >/dev/null 2>&1 && [[ $(ufw status 2>/dev/null) == *"Status: active"* ]]; then
  step "Opening ports 80 and 443 in ufw"
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
fi

for _ in $(seq 1 30); do
  if systemctl is-active --quiet sagand && [ -S /run/sagand/sagand.sock ]; then
    echo "✔ sagand is running"
    exit 0
  fi
  sleep 1
done
journalctl -u sagand -n 30 --no-pager >&2
fail "sagand did not start (its last log lines are above)."
