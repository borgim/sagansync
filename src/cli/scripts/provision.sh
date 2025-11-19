#!/usr/bin/env bash
set -euo pipefail

CYAN='\033[0;36m'
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a 
export APT_LISTCHANGES_FRONTEND=none

TARGET_USER="${SSH_USER:-$(whoami)}"
TARGET_UID="$(id -u)"
REMOTE_PATH="${REMOTE_PATH:-/opt/sagansync}"
CLEAN_INSTALL="${CLEAN_INSTALL:-false}"

log() { echo -e "${CYAN}[SAGAN]${NC} $1"; }
success() { echo -e "${GREEN}[✔] $1${NC}"; }
error() { echo -e "${RED}[✖] ERRO: $1${NC}" >&2; }

cleanup_if_requested() {
  if [ "$CLEAN_INSTALL" != "true" ]; then
    return 0
  fi

  log "🧹 Clean mode: Removing previous installations..."

  if command -v caddy &> /dev/null; then
    log "Removing Caddy..."
    sudo systemctl stop caddy > /dev/null 2>&1 || true
    sudo apt-get purge -y -qq caddy > /dev/null 2>&1
    sudo rm -rf /etc/caddy /usr/share/caddy
    sudo rm -f /etc/apt/sources.list.d/caddy-stable.list
    sudo rm -f /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  fi

  if command -v podman &> /dev/null; then
    log "Removing Podman..."
    podman stop -a > /dev/null 2>&1 || true
    podman rm -a > /dev/null 2>&1 || true
    
    sudo apt-get purge -y -qq podman uidmap slirp4netns > /dev/null 2>&1
    sudo rm -rf /etc/cni/net.d
  fi
  
  sudo apt-get autoremove -y -qq > /dev/null 2>&1
  
  success "Cleanup finished. Starting clean installation."
}

sudo_setup() {
  log "Updating system and installing dependencies..."
  sudo apt-get update -qq > /dev/null 2>&1
  sudo apt-get install -y -qq curl gnupg ca-certificates uidmap slirp4netns dbus-user-session > /dev/null 2>&1
}

install_podman() {
  if command -v podman &> /dev/null; then
    success "Podman already installed."
    return
  fi

  log "Installing Podman..."
  if [ -f /etc/os-release ]; then
    . /etc/os-release
  fi
  sudo apt-get install -y -qq podman > /dev/null 2>&1
}

install_caddy() {
  if command -v caddy &> /dev/null; then
    success "Caddy already installed."
    return
  fi
  
  log "Installing Caddy..."
  sudo apt-get install -y -qq debian-keyring debian-archive-keyring apt-transport-https > /dev/null 2>&1
  
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg --yes > /dev/null 2>&1
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list > /dev/null
  
  sudo apt-get update -qq > /dev/null 2>&1
  sudo apt-get install -y -qq caddy > /dev/null 2>&1
}

configure_user_env() {
  log "Configuring environment for user: $TARGET_USER"

  sudo mkdir -p "$REMOTE_PATH"
  sudo chown "$TARGET_USER:$TARGET_USER" "$REMOTE_PATH"

  if [ "$TARGET_UID" -eq 0 ]; then
    # === ROOT USER ===
    # Root uses system socket, not systemctl --user
    systemctl enable --now podman.socket > /dev/null 2>&1 || true
    
  else
    # Activate Linger
    if ! loginctl show-user "$TARGET_USER" 2>/dev/null | grep -q "Linger=yes"; then
      sudo loginctl enable-linger "$TARGET_USER" > /dev/null 2>&1 || true
    fi

    # Define runtime directory
    export XDG_RUNTIME_DIR="/run/user/$TARGET_UID"
    
    # Activate user socket
    systemctl --user enable --now podman.socket > /dev/null 2>&1 || true
  fi
}

export PATH=$PATH:/usr/local/bin:/usr/sbin

cleanup_if_requested
sudo_setup
install_podman
install_caddy
configure_user_env

log "Provisioning finished successfully!"
echo -e "   🔹 Podman: $(podman --version)"
echo -e "   🔹 Caddy: $(caddy version | awk '{print $1}')"