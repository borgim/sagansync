#!/usr/bin/env bash
# SaganSync - Provision script (Ubuntu/Debian)
# Execute com REMOTE_PATH definido no ambiente.
# Ex.: REMOTE_PATH=/opt/sagansync ./provision.sh
# Rodando via CLI: enviar este arquivo por stdin e usar `bash -lc 'REMOTE_PATH="${REMOTE_PATH}" bash -s'`

set -euo pipefail

export DEBIAN_FRONTEND=${DEBIAN_FRONTEND:-noninteractive}

SUDO="sudo"
if [ "$(id -u)" -eq 0 ]; then SUDO=""; fi

log()  { printf "\033[1;36m==>\033[0m %s\n" "$1"; }
ok()   { printf "\033[1;32m✔\033[0m %s\n" "$1"; }
warn() { printf "\033[1;33m!\033[0m %s\n" "$1"; }
err()  { printf "\033[1;31m✖\033[0m %s\n" "$1"; }

# -------- Helpers --------
has() { command -v "$1" >/dev/null 2>&1; }

ensure_apt() {
  if ! has apt-get; then
    err "apt-get não encontrado. Suporte atual: Ubuntu/Debian."
    exit 2
  fi
  $SUDO apt-get update -y -qq
}

ensure_pkgs() {
  # instala pacotes se faltarem
  local to_install=()
  for p in "$@"; do
    dpkg -s "$p" >/dev/null 2>&1 || to_install+=("$p")
  done
  if [ "${#to_install[@]}" -gt 0 ]; then
    $SUDO apt-get install -y -qq "${to_install[@]}"
  fi
}

# -------- Detecta OS --------
OS_ID=""
OS_VERSION_ID=""
OS_CODENAME=""
if [ -f /etc/os-release ]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  OS_ID="${ID:-}"
  OS_VERSION_ID="${VERSION_ID:-}"
  OS_CODENAME="${VERSION_CODENAME:-}"
fi
if [ -z "$OS_ID" ]; then
  warn "Não foi possível detectar o OS; assumindo Debian-like."
  OS_ID="debian"
fi
log "OS: $OS_ID ${OS_VERSION_ID:-} ${OS_CODENAME:-}"

# -------- Podman (rootless) --------
install_podman_native() {
  log "Instalando Podman (pacotes nativos) + deps rootless…"
  ensure_apt
  ensure_pkgs podman uidmap slirp4netns fuse-overlayfs
  has podman && ok "Podman instalado (nativo)." || return 1
}

install_podman_kubic_repo() {
  # tenta usar repositório Kubic (OpenSUSE) para versões mais novas do Podman
  log "Tentando instalar Podman via repositório Kubic…"
  ensure_apt
  ensure_pkgs curl gnupg ca-certificates

  case "$OS_ID" in
    ubuntu)
      if [ -z "$OS_VERSION_ID" ]; then
        warn "Ubuntu VERSION_ID indisponível; mantendo nativo."
        return 1
      fi
      $SUDO sh -c "echo 'deb [arch=amd64 signed-by=/usr/share/keyrings/libcontainers-archive-keyring.gpg] https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/xUbuntu_${OS_VERSION_ID}/ /' > /etc/apt/sources.list.d/devel:kubic:libcontainers:stable.list"
      curl -fsSL "https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/xUbuntu_${OS_VERSION_ID}/Release.key" | $SUDO gpg --dearmor -o /usr/share/keyrings/libcontainers-archive-keyring.gpg
      ;;
    debian)
      if [ -z "$OS_VERSION_ID" ]; then
        warn "Debian VERSION_ID indisponível; mantendo nativo."
        return 1
      fi
      MAJOR="${OS_VERSION_ID%%.*}"
      $SUDO sh -c "echo 'deb [signed-by=/usr/share/keyrings/libcontainers-archive-keyring.gpg] https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/Debian_${MAJOR}/ /' > /etc/apt/sources.list.d/devel:kubic:libcontainers:stable.list"
      curl -fsSL "https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/Debian_${MAJOR}/Release.key" | $SUDO gpg --dearmor -o /usr/share/keyrings/libcontainers-archive-keyring.gpg
      ;;
    *)
      warn "Distro não suportada para Kubic; abortando fallback."
      return 1
      ;;
  esac

  $SUDO apt-get update -y -qq
  ensure_pkgs podman uidmap slirp4netns fuse-overlayfs
  has podman && ok "Podman instalado (Kubic)." || return 1
}

configure_rootless_podman() {
  # Habilita linger (opcional) para user services sem sessão
  if has loginctl; then
    if ! loginctl show-user "$(id -un)" 2>/dev/null | grep -q 'Linger=yes'; then
      log "Habilitando user linger para $(id -un)…"
      $SUDO loginctl enable-linger "$(id -un)" || warn "Não foi possível habilitar linger (não fatal)."
    fi
  fi

  # storage.conf com overlay/fuse-overlayfs
  local runroot="/run/user/$(id -u)/containers"
  local graphroot="$HOME/.local/share/containers/storage"
  mkdir -p "$HOME/.config/containers"
  if [ ! -f "$HOME/.config/containers/storage.conf" ]; then
    cat > "$HOME/.config/containers/storage.conf" <<EOF
[storage]
driver = "overlay"
runroot = "${runroot}"
graphroot = "${graphroot}"
[storage.options]
mount_program = "/usr/bin/fuse-overlayfs"
EOF
    ok "storage.conf criado para rootless."
  else
    ok "storage.conf já existe (mantido)."
  fi
}

install_podman() {
  if has podman; then
    ok "Podman já instalado."
    return
  fi
  install_podman_native || install_podman_kubic_repo || {
    err "Falha ao instalar Podman."
    exit 1
  }
  configure_rootless_podman
}

# -------- Caddy --------
install_caddy() {
  if has caddy; then
    ok "Caddy já instalado."
    if has systemctl; then
      $SUDO systemctl enable --now caddy >/dev/null 2>&1 || true
    fi
    return
  fi

  log "Instalando Caddy (repo oficial)…"
  ensure_apt
  ensure_pkgs debian-keyring debian-archive-keyring apt-transport-https curl gnupg

  if [ ! -f /usr/share/keyrings/caddy-stable-archive-keyring.gpg ]; then
    curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | $SUDO gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | $SUDO tee /etc/apt/sources.list.d/caddy-stable.list >/dev/null
    $SUDO apt-get update -y -qq
  fi

  ensure_pkgs caddy
  if has systemctl; then
    $SUDO systemctl enable --now caddy
  fi

  if has caddy; then
    ok "Caddy instalado e iniciado."
  else
    err "Falha ao instalar Caddy."
    exit 1
  fi

  # Cria Caddyfile default se vazio/inexistente
  if [ ! -s /etc/caddy/Caddyfile ]; then
    $SUDO bash -lc 'cat > /etc/caddy/Caddyfile <<EOF
:80 {
  respond 200 "Caddy is up on :80"
}
EOF'
    if has systemctl; then
      $SUDO systemctl reload caddy || true
    fi
    ok "Caddyfile padrão criado."
  fi
}

# -------- Firewall (opcional) --------
maybe_configure_firewall() {
  if has ufw; then
    if $SUDO ufw status 2>/dev/null | grep -q "Status: active"; then
      log "UFW ativo; liberando 80/tcp e 443/tcp…"
      $SUDO ufw allow 80/tcp || true
      $SUDO ufw allow 443/tcp || true
      ok "Regras HTTP/HTTPS adicionadas."
    fi
  fi
}

# -------- Remote path --------
ensure_remote_path() {
  local path="${REMOTE_PATH:-}"
  if [ -z "$path" ]; then
    err "REMOTE_PATH não definido."
    exit 1
  fi
  log "Garantindo caminho remoto: $path"
  $SUDO mkdir -p "$path"
  $SUDO chown "$(id -un)":"$(id -gn)" "$path" || true
  ok "Caminho remoto pronto."
}

# -------- Execução --------
install_podman
install_caddy
maybe_configure_firewall
ensure_remote_path

log "Podman version: $(podman --version || true)"
log "Caddy version: $(caddy version || true)"
ok "Provision finalizado."
