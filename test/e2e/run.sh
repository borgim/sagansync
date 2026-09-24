#!/usr/bin/env bash
# End-to-end test for SaganSync (spec section 12.4).
#
# Creates a fresh Lima VM (Ubuntu 24.04), installs the agent with the real
# `sagansync provision`, runs Pebble as the ACME server, and checks the spec's
# success criteria with the real CLI: HTTPS deploy, zero downtime, rollback of
# a broken release, the deploy key's restrictions, env vars, dev mode on a
# preview domain, remove, the Podman integration test, upgrade and reboot.
#
# Usage: test/e2e/run.sh [--keep]
#   --keep  leave the VM running afterwards (delete it with: limactl delete -f sagansync-e2e)
#
# Needs: limactl, go, node >= 22.12, npm. Takes about 5 minutes.
set -euo pipefail

VM=sagansync-e2e
KEEP=0
[ "${1:-}" = "--keep" ] && KEEP=1
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
W=$(mktemp -d /tmp/sgs-e2e.XXXXXX) # short path: macOS limits socket paths to 104 bytes
CLI="$ROOT/cli/dist/main.js"
PASSED=0
FAILED=0

cleanup() {
  [ "$KEEP" -eq 1 ] || limactl delete -f "$VM" >/dev/null 2>&1 || true
  rm -rf "$W"
}
trap cleanup EXIT

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
pass() { PASSED=$((PASSED + 1)); printf '  \033[32m✔\033[0m %s\n' "$*"; }
fail() { FAILED=$((FAILED + 1)); printf '  \033[31m✖\033[0m %s\n' "$*"; }
# expect <description> <expected substring> <actual>
expect() { if [[ "$3" == *"$2"* ]]; then pass "$1"; else fail "$1 (expected \"$2\", got \"$3\")"; fi; }

vm() { limactl shell "$VM" -- bash -s; }
ssh_port() { limactl list --format '{{.SSHLocalPort}}' "$VM"; }
sagansync() { (cd "$W/app" && SAGANSYNC_HOME="$W/home" node "$CLI" "$@"); }
# get <host> [curl args...]: fetch https://<host>/ inside the VM, trusting Pebble's root
get() {
  local host=$1
  shift
  vm <<EOF
curl -s -m 5 --cacert /var/tmp/pebble-root.pem --resolve $host:443:127.0.0.1 $* https://$host/ || echo "curl failed (\$?)"
EOF
}

for bin in limactl go node npm; do command -v "$bin" >/dev/null || { echo "missing $bin"; exit 1; }; done

say "Building"
(cd "$ROOT/cli" && { [ -d node_modules ] || npm ci --silent; } && npm run build >/dev/null)
start_vm() {
  limactl delete -f "$VM" >/dev/null 2>&1 || true
  limactl start --name "$VM" --tty=false --cpus 4 --memory 4 --set '.mounts=[]' template://ubuntu-24.04 >"$W/lima.log" 2>&1
}
# A fresh VM occasionally fails to boot in time; one retry tells that apart
# from a real test failure.
start_vm || { echo "The VM did not boot; retrying once."; start_vm; } || { cat "$W/lima.log"; exit 1; }
case $(limactl shell "$VM" -- uname -m) in
  x86_64) GOARCH=amd64 ;;
  aarch64) GOARCH=arm64 ;;
  *) echo "unsupported VM architecture"; exit 1 ;;
esac
(cd "$ROOT/agent" && GOOS=linux GOARCH=$GOARCH go build -ldflags "-X main.version=$(node -p "require('$ROOT/cli/package.json').version")" -o "$W/sagand" ./cmd/sagand)
(cd "$ROOT/agent" && GOOS=linux GOARCH=$GOARCH go test -c -tags integration -o "$W/podman.test" ./internal/podman/)

mkdir -p "$W/home/keys" && chmod 700 "$W/home"
ssh-keygen -q -t ed25519 -N "" -C sagansync-e2e -f "$W/home/keys/127.0.0.1_ed25519"
cp -R "$ROOT/test/e2e/app" "$W/app"
(cd "$W/app" && git init -q -b main && git add . && git -c user.email=e2e@test -c user.name=e2e commit -qm v1)
mkdir -p "$W/app/.sagansync"
write_config() {
  cat >"$W/app/.sagansync/config.json" <<EOF
{ "host": "127.0.0.1", "sshPort": $(ssh_port), "project": "app", "internalPort": 3000,
  "domain": "app.test", "previewDomain": "preview.test", "healthPath": "/health" }
EOF
}
write_config
ADMIN=(--admin "$USER@127.0.0.1" --admin-key "$HOME/.lima/_config/user" --agent-binary "$W/sagand")

say "Provision"
out=$(sagansync provision "${ADMIN[@]}" 2>&1) || true
expect "provision installs sagand and reaches it with the deploy key" "is ready" "$out"
vm <<'EOF' >/dev/null 2>&1
sudo podman run -d --name pebble --net host -e PEBBLE_VA_ALWAYS_VALID=1 ghcr.io/letsencrypt/pebble:latest
sleep 3
sudo podman cp pebble:/test/certs/pebble.minica.pem /var/tmp/pebble.minica.pem
sudo chmod 644 /var/tmp/pebble.minica.pem
curl -s --cacert /var/tmp/pebble.minica.pem https://localhost:15000/roots/0 -o /var/tmp/pebble-root.pem
EOF
limactl copy "$VM:/var/tmp/pebble.minica.pem" "$W/pebble.minica.pem"
out=$(sagansync provision "${ADMIN[@]}" --acme-ca https://localhost:14000/dir --acme-root-ca "$W/pebble.minica.pem" 2>&1) || true
expect "provision can run again (now with Pebble as the ACME server)" "is ready" "$out"

say "Deploy key restrictions"
K=(-i "$W/home/keys/127.0.0.1_ed25519" -o IdentitiesOnly=yes -o BatchMode=yes -o "UserKnownHostsFile=$W/home/known_hosts" -p "$(ssh_port)" -l sagan)
out=$(ssh "${K[@]}" 127.0.0.1 bash -c id 2>&1; echo "exit=$?")
expect "a shell command is refused" "exit=126" "$out"
ssh "${K[@]}" -N -L 18080:127.0.0.1:443 127.0.0.1 >/dev/null 2>&1 &
FWD=$!
sleep 2
out=$(curl -s -m 2 -o /dev/null -w "%{http_code}" -k https://127.0.0.1:18080/ || true)
kill "$FWD" 2>/dev/null || true
expect "port forwarding is refused" "000" "$out"
out=$(vm <<<'sudo -u sagan sudo -n true 2>&1 || true')
expect "the sagan user has no sudo" "password is required" "$out"

say "Deploy"
out=$(sagansync deploy 2>&1) || true
expect "deploy reports the URL" "Live at https://app.test" "$out"
expect "the app answers over HTTPS with a valid certificate" "v1" "$(get app.test)"
out=$(vm <<<'curl -s -o /dev/null -w "%{http_code} %{redirect_url}" --resolve app.test:80:127.0.0.1 http://app.test/x')
expect "HTTP redirects to HTTPS" "308 https://app.test/x" "$out"
expect "an unknown host gets no certificate" "curl failed" "$(get nope.test)"

say "Zero downtime"
out=$(sagansync env set APP_VERSION=v2 "GREETING=it's \"quoted\" \$HOME" 2>&1) || true
expect "env set" "Set APP_VERSION, GREETING" "$out"
vm <<'EOF'
rm -f /var/tmp/loop.log /var/tmp/loop.stop
nohup bash -c 'while [ ! -f /var/tmp/loop.stop ]; do curl -s -m 2 -o /dev/null -w "%{http_code}\n" --cacert /var/tmp/pebble-root.pem --resolve app.test:443:127.0.0.1 https://app.test/ >> /var/tmp/loop.log; sleep 0.1; done' >/dev/null 2>&1 &
EOF
sleep 2
sagansync deploy >/dev/null 2>&1 || true
sleep 2
# shellcheck disable=SC2016 # expanded inside the VM
out=$(vm <<<'touch /var/tmp/loop.stop; sleep 1; echo "total=$(wc -l < /var/tmp/loop.log) failed=$(grep -vc "^200$" /var/tmp/loop.log)"')
expect "no request failed while v2 replaced v1 ($out)" "failed=0" "$out"
expect "v2 is live with env values intact" "v2 it's \"quoted\" \$HOME" "$(get app.test)"

say "Broken release"
cp "$W/app/server.js" "$W/server.js.good"
printf 'console.error("boom: missing DATABASE_URL"); process.exit(1);\n' >"$W/app/server.js"
out=$(sagansync deploy 2>&1; echo "exit=$?")
cp "$W/server.js.good" "$W/app/server.js"
expect "a crashing release fails the deploy" "exit=1" "$out"
expect "the crash logs are shown" "boom: missing DATABASE_URL" "$out"
expect "the previous release keeps serving" "v2" "$(get app.test)"

say "list and logs"
expect "list shows production running" "running" "$(sagansync list 2>&1)"
expect "logs show the app's output" "request /" "$(sagansync logs -n 20 2>&1)"

say "Dev mode on a preview domain"
(cd "$W/app" && git checkout -q -b feat-x)
(cd "$W/app" && SAGANSYNC_HOME="$W/home" exec node "$CLI" dev -c "node --watch server.js") >"$W/dev.log" 2>&1 &
DEV=$!
for _ in $(seq 1 120); do grep -q "Watching for changes" "$W/dev.log" && break; sleep 1; done
expect "dev starts on the preview host" "Dev server at https://feat-x-app.preview.test" "$(cat "$W/dev.log")"
sed -i.bak 's/const version = process.env.APP_VERSION || "v1";/const version = "dev edit";/' "$W/app/server.js"
out=""
for _ in $(seq 1 20); do
  out=$(get feat-x-app.preview.test)
  [[ "$out" == *"dev edit"* ]] && break
  sleep 1
done
expect "a local edit shows up on the preview URL" "dev edit" "$out"
kill "$DEV" 2>/dev/null || true
wait "$DEV" 2>/dev/null || true
mv "$W/app/server.js.bak" "$W/app/server.js"
out=$(sagansync remove -w feat-x --yes 2>&1) || true
expect "remove deletes the workspace" "Removed app/feat-x" "$out"
expect "the preview host is gone" "404" "$(get feat-x-app.preview.test -k -o /dev/null -w '%{http_code}')"

say "Podman integration test"
limactl copy "$W/podman.test" "$VM:/var/tmp/podman.test"
out=$(vm <<'EOF'
sudo install -o sagan -m 0755 /var/tmp/podman.test /home/sagan/podman.test
# sagand's reconciliation removes managed containers it does not know about,
# which includes the test's container, so it is stopped while the test runs.
sudo systemctl stop sagand
sudo -iu sagan bash -c 'cd /home/sagan && SAGAN_PODMAN_SOCKET=/run/user/$(id -u)/podman/podman.sock ./podman.test -test.v -test.run TestAgainstRealPodman 2>&1 | tail -20'
sudo systemctl start sagand
EOF
)
expect "the Podman client works against real rootless Podman" "PASS" "$out"

say "Upgrade"
out=$(sagansync provision --upgrade "${ADMIN[@]}" 2>&1) || true
expect "provision --upgrade" "is ready" "$out"
expect "the app still serves after the upgrade" "v2" "$(get app.test)"

say "Reboot"
limactl stop "$VM" >/dev/null 2>&1
limactl start "$VM" >/dev/null 2>&1
write_config # Lima picks a new SSH port on every start
out=""
for _ in $(seq 1 60); do
  out=$(get app.test)
  [[ "$out" == *"v2"* ]] && break
  sleep 1
done
expect "the app comes back after a reboot" "v2" "$out"
expect "the CLI reaches the agent after the reboot" "running" "$(sagansync list 2>&1)"

printf '\n%d passed, %d failed\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
