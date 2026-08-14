#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
SERVICE_FILE="/etc/systemd/system/mc-wol-proxy.service"

# Overridable so the install can be pointed at a mirror, and so the installer
# itself can be tested without publishing a release.
INSTALL_DIR="${MC_WOL_INSTALL_DIR:-/opt/mc-wol-proxy}"
REPO="${MC_WOL_REPO:-posch-dev/minecraft-wake-on-demand}"
API_BASE="${MC_WOL_API_BASE:-https://api.github.com}"
DOWNLOAD_BASE="${MC_WOL_DOWNLOAD_BASE:-https://github.com}"

if [ "$EUID" -ne 0 ]; then
    echo "Please run as root: sudo ./install.sh"
    exit 1
fi

RUN_USER="${SUDO_USER:-$(whoami)}"
if [ "$RUN_USER" = "root" ]; then
    echo "WARNING: No SUDO_USER detected, service will run as root."
    echo "  Consider running with: sudo -E ./install.sh"
fi

if [ "$1" = "--uninstall" ]; then
    echo "Stopping and disabling mc-wol-proxy..."
    systemctl stop mc-wol-proxy 2>/dev/null || true
    systemctl disable mc-wol-proxy 2>/dev/null || true

    if [ -f "$SERVICE_FILE" ]; then
        rm "$SERVICE_FILE"
        echo "Removed $SERVICE_FILE"
    fi

    if [ -d "$INSTALL_DIR" ]; then
        if [ -f "$INSTALL_DIR/config.yml" ]; then
            read -r -p "Delete config at $INSTALL_DIR/config.yml? [y/N] " confirm
            if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
                echo "Keeping config. Removing everything else..."
                rm -f "$INSTALL_DIR/mc-wol-proxy" "$INSTALL_DIR/known_hosts"
                echo "Removed $INSTALL_DIR/mc-wol-proxy"
                # Remove dir only if empty
                rmdir "$INSTALL_DIR" 2>/dev/null || echo "Directory not empty, kept $INSTALL_DIR"
            else
                rm -rf "$INSTALL_DIR"
                echo "Removed $INSTALL_DIR"
            fi
        else
            rm -rf "$INSTALL_DIR"
            echo "Removed $INSTALL_DIR"
        fi
    fi

    systemctl daemon-reload
    echo "Uninstall complete."
    exit 0
fi

echo "=== Minecraft Wake-on-Demand Proxy Installer ==="

case "$(uname -m)" in
    x86_64|amd64)   GOARCH="amd64" ;;
    aarch64|arm64)  GOARCH="arm64" ;;
    armv7l|armv7)   GOARCH="armv7" ;;
    armv6l|armv6)   GOARCH="armv6" ;;
    *)
        echo "ERROR: Unsupported architecture $(uname -m)."
        echo "  Build from source instead: sudo ./install.sh --build"
        exit 1
        ;;
esac
echo "Architecture: $(uname -m) -> $GOARCH"

mkdir -p "$INSTALL_DIR"
BINARY="$INSTALL_DIR/mc-wol-proxy"

if [ "$1" = "--build" ]; then
    if ! command -v go &>/dev/null; then
        echo "ERROR: --build needs the Go toolchain, which is not installed."
        echo "  Install it with: sudo apt install golang-go"
        echo "  Or drop --build to download a prebuilt binary instead."
        exit 1
    fi
    echo "Building from source with $(go version | awk '{print $3}')..."
    ( cd "$SCRIPT_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BINARY" . )
    echo "Built $BINARY"
else
    # Downloads the release asset and refuses to install it unless the
    # checksum published alongside it matches.
    if command -v curl &>/dev/null; then
        FETCH="curl -fsSL"
    elif command -v wget &>/dev/null; then
        FETCH="wget -qO-"
    else
        echo "ERROR: Neither curl nor wget is available."
        echo "  Install one with: sudo apt install curl"
        exit 1
    fi

    echo "Looking up the latest release..."
    VERSION="$($FETCH "$API_BASE/repos/$REPO/releases/latest" \
        | grep -m1 '"tag_name"' | cut -d'"' -f4 || true)"
    if [ -z "$VERSION" ]; then
        echo "ERROR: Could not find a release to download."
        echo "  Build from source instead: sudo ./install.sh --build"
        exit 1
    fi
    echo "Latest release: $VERSION"

    ASSET="mc-wol-proxy_linux_${GOARCH}"
    BASE="$DOWNLOAD_BASE/$REPO/releases/download/$VERSION"
    TMP="$(mktemp -d)"
    trap 'rm -rf "$TMP"' EXIT

    echo "Downloading $ASSET..."
    if ! $FETCH "$BASE/$ASSET" > "$TMP/$ASSET"; then
        echo "ERROR: Download failed."
        echo "  Build from source instead: sudo ./install.sh --build"
        exit 1
    fi
    if ! $FETCH "$BASE/checksums.txt" > "$TMP/checksums.txt"; then
        echo "ERROR: Could not download checksums.txt, refusing to install unverified."
        exit 1
    fi

    # sha256sum writes "hash  name" in text mode and "hash *name" in binary
    # mode, so the name is compared with any leading star stripped.
    EXPECTED="$(awk -v want="$ASSET" '{ name = $2; sub(/^\*/, "", name); if (name == want) print $1 }' \
        "$TMP/checksums.txt")"
    ACTUAL="$(sha256sum "$TMP/$ASSET" | awk '{print $1}')"
    if [ -z "$EXPECTED" ]; then
        echo "ERROR: $ASSET is not listed in checksums.txt, refusing to install."
        exit 1
    fi
    if [ "$EXPECTED" != "$ACTUAL" ]; then
        echo "ERROR: Checksum mismatch, refusing to install."
        echo "  expected $EXPECTED"
        echo "  actual   $ACTUAL"
        exit 1
    fi
    echo "Checksum verified."

    install -m 755 "$TMP/$ASSET" "$BINARY"
    echo "Installed $BINARY"
fi

chmod 755 "$BINARY"

# never overwrite existing config
if [ ! -f "$INSTALL_DIR/config.yml" ]; then
    if [ -f "$REPO_DIR/config.yml" ]; then
        SOURCE_CONFIG="$REPO_DIR/config.yml"
    else
        SOURCE_CONFIG="$REPO_DIR/config.example.yml"
    fi
    cp "$SOURCE_CONFIG" "$INSTALL_DIR/config.yml"
    # The config holds the DuckDNS token, so only the service user may read it.
    chown "$RUN_USER:$RUN_USER" "$INSTALL_DIR/config.yml"
    chmod 600 "$INSTALL_DIR/config.yml"
    echo "Copied $(basename "$SOURCE_CONFIG") to $INSTALL_DIR/config.yml"
    NEEDS_CONFIG=1
else
    echo "Config already exists at $INSTALL_DIR/config.yml (not overwritten)"
    NEEDS_CONFIG=0
fi

# copy default assets (never overwrite existing ones)
mkdir -p "$INSTALL_DIR/assets"
for f in "$SCRIPT_DIR/assets"/*; do
    [ -f "$f" ] || continue
    dest="$INSTALL_DIR/assets/$(basename "$f")"
    if [ ! -f "$dest" ]; then
        cp "$f" "$dest"
    fi
done
chown -R "$RUN_USER:$RUN_USER" "$INSTALL_DIR/assets"

# known_hosts lives next to the binary so the unit can keep the home read only
touch "$INSTALL_DIR/known_hosts"
chown "$RUN_USER:$RUN_USER" "$INSTALL_DIR/known_hosts"
chmod 600 "$INSTALL_DIR/known_hosts"

# The unit ships with the default paths, so they follow INSTALL_DIR.
sed -e "s/MC_WOL_USER/$RUN_USER/g" -e "s|/opt/mc-wol-proxy|$INSTALL_DIR|g" \
    "$SCRIPT_DIR/mc-wol-proxy.service" > "$SERVICE_FILE"
systemctl daemon-reload
echo "Installed systemd service (running as $RUN_USER)"

if [ "$NEEDS_CONFIG" = "1" ]; then
    echo ""
    echo "=== Almost done ==="
    echo "There is no config yet. Fill one in with:"
    echo ""
    echo "  sudo MC_WOL_CONFIG=$INSTALL_DIR/config.yml $BINARY init"
    echo "  sudo MC_WOL_CONFIG=$INSTALL_DIR/config.yml $BINARY setup-ssh"
    echo ""
    echo "Then start it with: sudo systemctl enable --now mc-wol-proxy"
    exit 0
fi

systemctl enable mc-wol-proxy
systemctl restart mc-wol-proxy

echo ""
echo "=== Installation complete ==="
systemctl status mc-wol-proxy --no-pager || true
echo ""
echo "Check the setup: sudo MC_WOL_CONFIG=$INSTALL_DIR/config.yml $BINARY check"
echo "View logs:       journalctl -u mc-wol-proxy -f"
