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
    DEBIAN_FRONTEND=noninteractive apt-get purge -y -qq caddy >/dev/null
    rm -rf /etc/caddy
  else
    echo "$busy" >&2
    fail "Ports 80/443 are already in use (see above). sagand needs them for HTTPS: stop that service and run again."
  fi
fi

# --- Packages --------------------------------------------------------------------
if [ "$UPGRADE" -eq 0 ]; then
  step "Installing Podman"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq podman uidmap passt slirp4netns dbus-user-session >/dev/null
fi
podman_version=$(podman version --format '{{.Client.Version}}')
dpkg --compare-versions "$podman_version" ge 4.3 || fail "Podman $podman_version is too old: sagand needs 4.3 or newer."

# --- The sagan user ----------------------------------------------------------------
if ! id sagan >/dev/null 2>&1; then
  step "Creating the sagan user"
  useradd --system --create-home --home-dir /home/sagan --shell /bin/sh sagan
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
  install -o sagan -g sagan -m 0600 "$BUNDLE/config.json" /var/lib/sagand/config.json
fi
if [ -f "$BUNDLE/acme-root-ca.pem" ]; then
  install -o sagan -g sagan -m 0600 "$BUNDLE/acme-root-ca.pem" /var/lib/sagand/acme-root-ca.pem
fi

# --- Deploy key: it may only run the sagand gateway --------------------------------
ssh-keygen -l -f "$BUNDLE/deploy.pub" >/dev/null 2>&1 || fail "deploy.pub is not a valid SSH public key."
key=$(head -n 1 "$BUNDLE/deploy.pub")
line="command=\"/usr/local/bin/sagand gateway\",restrict $key"
install -d -o sagan -g sagan -m 0700 /home/sagan/.ssh
auth=/home/sagan/.ssh/authorized_keys
touch "$auth"
grep -qxF "$line" "$auth" || echo "$line" >> "$auth"
chown sagan:sagan "$auth"
chmod 0600 "$auth"

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

if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
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
