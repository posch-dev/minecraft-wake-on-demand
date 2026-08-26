#!/bin/bash
# Runs watcher/install.sh against a fake release served over local HTTP and
# checks what it produced, then the refusal paths, then the uninstall.
# Needs root and systemd. Set TEST_BINARY to the linux binary to publish.
set -u

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BINARY="${TEST_BINARY:?set TEST_BINARY to the linux binary to serve}"
PORT="${TEST_PORT:-8099}"
BASE="http://127.0.0.1:${PORT}"
WORK="$(mktemp -d)"
FAILURES=0

pass() { echo "  ok    $*"; }
fail() { echo "  FAIL  $*"; FAILURES=$((FAILURES + 1)); }
indent() { while IFS= read -r line; do echo "  | $line"; done; }

# ok_if <message> <command...>, passes when the command succeeds
ok_if() {
    local message="$1"
    shift
    if "$@"; then pass "$message"; else fail "$message"; fi
}

# fail_if <message> <command...>, the message states the good outcome and the
# check fails when the command succeeds
fail_if() {
    local message="$1"
    shift
    if "$@"; then fail "$message"; else pass "$message"; fi
}

# shellcheck disable=SC2317,SC2329  # runs from the EXIT trap below
cleanup() {
    if [ -f "$WORK/http.pid" ]; then
        kill "$(cat "$WORK/http.pid")" 2>/dev/null
    fi
    rm -rf "$WORK"
}
trap cleanup EXIT

if [ "$EUID" -ne 0 ]; then
    echo "run me as root, install.sh needs it"
    exit 1
fi
if ! systemctl --version >/dev/null 2>&1; then
    echo "no systemd here, this test needs it"
    exit 1
fi

echo "=== staging a fake release ==="
API_DIR="$WORK/release/repos/posch-dev/minecraft-wake-on-demand/releases"
DL_DIR="$WORK/release/posch-dev/minecraft-wake-on-demand/releases/download/v0.0.0-test"
mkdir -p "$API_DIR" "$DL_DIR"
echo '{"tag_name":"v0.0.0-test","name":"fake"}' > "$API_DIR/latest"
cp "$BINARY" "$DL_DIR/mc-wol-proxy_linux_amd64"
chmod +x "$DL_DIR/mc-wol-proxy_linux_amd64"
( cd "$DL_DIR" && sha256sum mc-wol-proxy_linux_amd64 > checksums.txt )

( cd "$WORK/release" && python3 -m http.server "$PORT" --bind 127.0.0.1 >"$WORK/http.log" 2>&1 &
  echo $! > "$WORK/http.pid" )
for _ in $(seq 1 20); do
    if curl -fsS "$BASE/repos/posch-dev/minecraft-wake-on-demand/releases/latest" >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done
if ! curl -fsS "$BASE/repos/posch-dev/minecraft-wake-on-demand/releases/latest" >/dev/null 2>&1; then
    echo "the fake release server never came up"
    cat "$WORK/http.log"
    exit 1
fi
pass "fake release server on $BASE"

export MC_WOL_API_BASE="$BASE"
export MC_WOL_DOWNLOAD_BASE="$BASE"
: "${SUDO_USER:=root}"
export SUDO_USER

echo ""
echo "=== 1. install ==="
"$REPO_ROOT/watcher/install.sh" 2>&1 | indent

echo ""
echo "=== 2. what it produced ==="
ok_if "binary installed and executable" test -x /opt/mc-wol-proxy/mc-wol-proxy
ok_if "config.yml placed" test -f /opt/mc-wol-proxy/config.yml
MODE="$(stat -c %a /opt/mc-wol-proxy/config.yml 2>/dev/null || echo none)"
ok_if "config.yml is mode 600, it holds the DuckDNS token (got $MODE)" test "$MODE" = "600"
ok_if "assets copied" test -f /opt/mc-wol-proxy/assets/motd-sleeping.json
ok_if "known_hosts created" test -f /opt/mc-wol-proxy/known_hosts
ok_if "unit installed" test -f /etc/systemd/system/mc-wol-proxy.service
ok_if "unit grants CAP_NET_RAW, otherwise ICMP falls back to the ping binary" \
    grep -q "AmbientCapabilities=CAP_NET_RAW" /etc/systemd/system/mc-wol-proxy.service
fail_if "the user placeholder was not substituted" \
    grep -q "MC_WOL_USER" /etc/systemd/system/mc-wol-proxy.service

echo ""
echo "=== 3. with a real config the service starts ==="
cat > /opt/mc-wol-proxy/config.yml <<'CFGEOF'
watcher:
  listen_address: "127.0.0.1"
  listen_port: 25599
server:
  mac: "01:23:45:67:89:AB"
  ip: "127.0.0.1"
  mc_port: 25598
  ssh_user: "nobody"
  container_name: "minecraft"
wol:
  mode: "broadcast"
  broadcast_address: "127.0.0.1"
duckdns:
  enabled: false
transfer:
  enabled: false
CFGEOF
chown "$SUDO_USER:$SUDO_USER" /opt/mc-wol-proxy/config.yml 2>/dev/null || true
chmod 600 /opt/mc-wol-proxy/config.yml

"$REPO_ROOT/watcher/install.sh" >"$WORK/install2.log" 2>&1 || true
for _ in $(seq 1 20); do
    if [ "$(systemctl is-active mc-wol-proxy 2>&1)" = "active" ]; then
        break
    fi
    sleep 0.5
done

STATE="$(systemctl is-active mc-wol-proxy 2>&1)"
ok_if "service is active (got $STATE)" test "$STATE" = "active"
ENABLED="$(systemctl is-enabled mc-wol-proxy 2>&1)"
ok_if "service enabled at boot (got $ENABLED)" test "$ENABLED" = "enabled"
echo "  --- journal ---"
journalctl -u mc-wol-proxy --no-pager -n 6 -o cat 2>/dev/null | indent

echo ""
echo "=== 4. it answers a real status ping ==="
ERR_FILE="$WORK/status-err.txt"
MOTD="$(python3 "$REPO_ROOT/.github/scripts/mc-status-ping.py" 127.0.0.1 25599 2>"$ERR_FILE")"
if [ -z "$MOTD" ]; then
    fail "no status response"
    cat "$ERR_FILE" | indent
else
    echo "$MOTD" | indent
    LINES="$(echo "$MOTD" | wc -l)"
    ok_if "MOTD renders two lines (got $LINES)" test "$LINES" -eq 2
    ok_if "it is the sleeping MOTD" grep -qi "asleep" <<<"$MOTD"
    ok_if "status ping reports a version" grep -q "version:" "$ERR_FILE"
fi

echo ""
echo "=== 5. a tampered binary is refused ==="
systemctl stop mc-wol-proxy 2>/dev/null
printf 'tampered' >> "$DL_DIR/mc-wol-proxy_linux_amd64"
if "$REPO_ROOT/watcher/install.sh" >"$WORK/tamper.log" 2>&1; then
    fail "a binary whose checksum does not match was installed"
else
    ok_if "refused, checksum mismatch" grep -q "Checksum mismatch" "$WORK/tamper.log"
fi

echo ""
echo "=== 6. an asset missing from checksums.txt is refused ==="
( cd "$DL_DIR" && sha256sum mc-wol-proxy_linux_amd64 > checksums.txt \
  && sed -i 's/mc-wol-proxy_linux_amd64/something_else/' checksums.txt )
if "$REPO_ROOT/watcher/install.sh" >"$WORK/unlisted.log" 2>&1; then
    fail "an unlisted asset was installed"
else
    ok_if "refused, not listed in checksums.txt" grep -q "not listed in checksums.txt" "$WORK/unlisted.log"
fi

echo ""
echo "=== 7. uninstall ==="
echo y | "$REPO_ROOT/watcher/install.sh" --uninstall 2>&1 | indent
fail_if "unit removed" test -f /etc/systemd/system/mc-wol-proxy.service
fail_if "install directory removed" test -d /opt/mc-wol-proxy

echo ""
if [ "$FAILURES" -eq 0 ]; then
    echo "ALL CHECKS PASSED"
else
    echo "$FAILURES CHECK(S) FAILED"
fi
exit "$FAILURES"
