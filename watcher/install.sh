#!/bin/bash
set -e

INSTALL_DIR="/opt/mc-wol-proxy"
SERVICE_FILE="/etc/systemd/system/mc-wol-proxy.service"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"

if [ "$EUID" -ne 0 ]; then
    echo "Please run as root: sudo ./install.sh"
    exit 1
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
                rm -f "$INSTALL_DIR/mc_wol_proxy.py"
                echo "Removed $INSTALL_DIR/mc_wol_proxy.py"
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

if ! command -v python3 &>/dev/null; then
    echo "ERROR: Python 3 is required but not installed."
    echo "  Install with: sudo apt install python3 python3-pip"
    exit 1
fi

if ! python3 -c "import yaml" 2>/dev/null; then
    echo "Installing pyyaml..."
    pip3 install pyyaml || python3 -m pip install pyyaml
fi

missing=""
if ! command -v ping &>/dev/null; then
    missing="$missing iputils-ping"
fi
if ! command -v ssh &>/dev/null; then
    missing="$missing openssh-client"
fi
if [ -n "$missing" ]; then
    echo "WARNING: Missing packages:$missing"
    echo "  Install with: sudo apt install$missing"
    exit 1
fi

mkdir -p "$INSTALL_DIR"

cp "$SCRIPT_DIR/mc_wol_proxy.py" "$INSTALL_DIR/mc_wol_proxy.py"
echo "Copied mc_wol_proxy.py to $INSTALL_DIR/"

# never overwrite existing config
if [ ! -f "$INSTALL_DIR/config.yml" ]; then
    cp "$REPO_DIR/config.yml" "$INSTALL_DIR/config.yml"
    echo "Copied config.yml to $INSTALL_DIR/"
    echo "  >>> Edit $INSTALL_DIR/config.yml with your settings! <<<"
else
    echo "Config already exists at $INSTALL_DIR/config.yml (not overwritten)"
fi

cp "$SCRIPT_DIR/mc-wol-proxy.service" "$SERVICE_FILE"
echo "Installed systemd service"

systemctl daemon-reload
systemctl enable mc-wol-proxy
systemctl start mc-wol-proxy

echo ""
echo "=== Installation complete ==="
systemctl status mc-wol-proxy --no-pager || true
echo ""
echo "View logs: journalctl -u mc-wol-proxy -f"
