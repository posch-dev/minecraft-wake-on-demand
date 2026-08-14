#!/bin/bash
# Builds the watcher image and checks that it starts, loads a config and
# answers a real Minecraft status ping. Needs docker.
set -u

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TAG="mc-wol-proxy:citest"
NAME="mc-wol-smoke"
PORT="${TEST_PORT:-25599}"
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

# shellcheck disable=SC2317,SC2329  # runs from the EXIT trap below
cleanup() {
    docker rm -f "$NAME" >/dev/null 2>&1
    docker rmi "$TAG" >/dev/null 2>&1
    rm -rf "$WORK"
}
trap cleanup EXIT

echo "=== build ==="
if ! docker build --build-arg VERSION=v0.0.0-citest -t "$TAG" "$REPO_ROOT/watcher" >"$WORK/build.log" 2>&1; then
    tail -30 "$WORK/build.log"
    echo "FAIL the image does not build"
    exit 1
fi
pass "image builds"

SIZE="$(docker image inspect "$TAG" --format '{{.Size}}')"
SIZE_MB=$((SIZE / 1024 / 1024))
# The whole point of the scratch base. A jump here means something large crept
# back into the image.
ok_if "image stays small (${SIZE_MB} MB)" test "$SIZE_MB" -lt 40

cat > "$WORK/config.yml" <<'CFGEOF'
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

echo ""
echo "=== run ==="
if ! docker run -d --name "$NAME" --network host --cap-add NET_RAW \
        -v "$WORK/config.yml:/config.yml:ro" "$TAG" >/dev/null 2>"$WORK/run.log"; then
    cat "$WORK/run.log"
    echo "FAIL the container does not start"
    exit 1
fi

for _ in $(seq 1 30); do
    if docker logs "$NAME" 2>&1 | grep -q "listening on"; then
        break
    fi
    sleep 0.5
done
echo "  --- container log ---"
docker logs "$NAME" 2>&1 | head -8 | indent

RUNNING="$(docker inspect -f '{{.State.Running}}' "$NAME" 2>&1)"
ok_if "container stays running (got $RUNNING)" test "$RUNNING" = "true"
ok_if "the version was compiled in" grep -q "v0.0.0-citest" <(docker logs "$NAME" 2>&1)

echo ""
echo "=== status ping against the container ==="
MOTD="$(python3 "$REPO_ROOT/.github/scripts/mc-status-ping.py" 127.0.0.1 "$PORT" 2>/dev/null)"
if [ -z "$MOTD" ]; then
    fail "no status response from the container"
else
    echo "$MOTD" | indent
    LINES="$(echo "$MOTD" | wc -l)"
    ok_if "MOTD renders two lines (got $LINES)" test "$LINES" -eq 2
    ok_if "it is the sleeping MOTD" grep -qi "asleep" <<<"$MOTD"
fi

echo ""
if [ "$FAILURES" -eq 0 ]; then
    echo "ALL CHECKS PASSED"
else
    echo "$FAILURES CHECK(S) FAILED"
fi
exit "$FAILURES"
