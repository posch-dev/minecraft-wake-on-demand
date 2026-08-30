#!/bin/bash
set -e

# Piped into a shell there is no checkout to read from, so this script only
# fetches a verified binary and lets 'mcwod install' do the rest.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || echo "")"
SERVICE_FILE="/etc/systemd/system/mcwod.service"

# Overridable so the install can be pointed at a mirror, and so the installer
# itself can be tested without publishing a release.
# The tool was called mc-wol-proxy until 2.1, its old variables still work.
INSTALL_DIR="${MCWOD_INSTALL_DIR:-${MC_WOL_INSTALL_DIR:-/opt/mcwod}}"
REPO="${MCWOD_REPO:-${MC_WOL_REPO:-posch-dev/minecraft-wake-on-demand}}"
API_BASE="${MCWOD_API_BASE:-${MC_WOL_API_BASE:-https://api.github.com}}"
DOWNLOAD_BASE="${MCWOD_DOWNLOAD_BASE:-${MC_WOL_DOWNLOAD_BASE:-https://github.com}}"

if [ "$EUID" -ne 0 ]; then
    echo "Please run as root: curl -fsSL <url> | sudo bash"
    exit 1
fi

RUN_USER="${SUDO_USER:-$(whoami)}"
if [ "$RUN_USER" = "root" ]; then
    echo "WARNING: No SUDO_USER detected, service will run as root."
    echo "  Consider running it with sudo from your own account instead."
fi

if [ "$1" = "--uninstall" ]; then
    echo "Stopping and disabling mcwod..."
    systemctl stop mcwod 2>/dev/null || true
    systemctl disable mcwod 2>/dev/null || true

    if [ -f "$SERVICE_FILE" ]; then
        rm "$SERVICE_FILE"
        echo "Removed $SERVICE_FILE"
    fi

    if [ -d "$INSTALL_DIR" ]; then
        if [ -f "$INSTALL_DIR/config.yml" ]; then
            read -r -p "Delete config at $INSTALL_DIR/config.yml? [y/N] " confirm
            if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
                echo "Keeping config. Removing everything else..."
                rm -f "$INSTALL_DIR/mcwod" "$INSTALL_DIR/known_hosts"
                echo "Removed $INSTALL_DIR/mcwod"
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

echo "=== Minecraft Wake-on-Demand installer ==="

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BINARY="$TMP/mcwod"

if [ "$1" = "--build" ]; then
    if [ -z "$SCRIPT_DIR" ] || [ ! -f "$SCRIPT_DIR/main.go" ]; then
        echo "ERROR: --build needs the repository, and this script was piped in."
        echo "  Clone it first, then run watcher/install.sh --build from there."
        exit 1
    fi
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
    case "$(uname -m)" in
        x86_64|amd64)   GOARCH="amd64" ;;
        aarch64|arm64)  GOARCH="arm64" ;;
        armv7l|armv7)   GOARCH="armv7" ;;
        armv6l|armv6)   GOARCH="armv6" ;;
        *)
            echo "ERROR: Unsupported architecture $(uname -m)."
            echo "  Build from source instead: ./install.sh --build"
            exit 1
            ;;
    esac
    echo "Architecture: $(uname -m) -> $GOARCH"

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
    # Fetched into a variable first. Piping straight into grep -m1 closes the
    # pipe early, and curl then prints a write error that reads like a crash.
    RELEASE_JSON="$($FETCH "$API_BASE/repos/$REPO/releases/latest" || true)"
    VERSION="$(printf '%s' "$RELEASE_JSON" | grep -m1 '"tag_name"' | cut -d'"' -f4 || true)"
    if [ -z "$VERSION" ]; then
        echo "ERROR: Could not find a release to download."
        echo "  Build from source instead: ./install.sh --build"
        exit 1
    fi
    echo "Latest release: $VERSION"

    ASSET="mcwod_linux_${GOARCH}"
    BASE="$DOWNLOAD_BASE/$REPO/releases/download/$VERSION"

    echo "Downloading $ASSET..."
    if ! $FETCH "$BASE/$ASSET" > "$TMP/$ASSET"; then
        echo "ERROR: Download failed."
        echo "  Build from source instead: ./install.sh --build"
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
fi

# The binary carries the unit file and the example assets, so from here on it
# installs itself and everything below INSTALL_DIR belongs to RUN_USER.
MCWOD_INSTALL_DIR="$INSTALL_DIR" "$BINARY" install
