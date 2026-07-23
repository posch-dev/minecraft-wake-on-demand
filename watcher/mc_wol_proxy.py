#!/usr/bin/env python3
"""Minecraft Wake-on-Demand Proxy"""

import asyncio
import base64
import json
import logging
import os
import signal
import socket
import struct
import subprocess
import sys
from pathlib import Path
from urllib.request import urlopen
from urllib.error import URLError

import yaml

IS_WINDOWS = sys.platform == "win32"
IS_LINUX = sys.platform == "linux"

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
    datefmt="%Y-%m-%d %H:%M:%S",
)
log = logging.getLogger("mc-wol-proxy")

def load_config():
    paths = [
        os.environ.get("MC_WOL_CONFIG"),
        str(Path(__file__).resolve().parent.parent / "config.yml"),
        str(Path(__file__).resolve().parent / "config.yml"),
    ]
    for p in paths:
        if p and os.path.isfile(p):
            log.info("Loading config from %s", p)
            with open(p, "r", encoding="utf-8") as f:
                return yaml.safe_load(f)
    log.error("No config.yml found. Searched: %s", [p for p in paths if p])
    sys.exit(1)


def env_override(cfg):
    mapping = {
        "SERVER_MAC": ("server", "mac"),
        "SERVER_IP": ("server", "ip"),
        "SERVER_MC_PORT": ("server", "mc_port", int),
        "SERVER_SSH_USER": ("server", "ssh_user"),
        "SERVER_SSH_KEY_PATH": ("server", "ssh_key_path"),
        "SERVER_CONTAINER_NAME": ("server", "container_name"),
        "WATCHER_LISTEN_PORT": ("watcher", "listen_port", int),
        "WOL_MODE": ("wol", "mode"),
        "WOL_BROADCAST_ADDRESS": ("wol", "broadcast_address"),
        "DUCKDNS_ENABLED": ("duckdns", "enabled", lambda v: v.lower() == "true"),
        "DUCKDNS_DOMAIN": ("duckdns", "domain"),
        "DUCKDNS_TOKEN": ("duckdns", "token"),
        "DUCKDNS_UPDATE_INTERVAL_HOURS": ("duckdns", "update_interval_hours", int),
        "BOOT_TIMEOUT": ("timeouts", "boot_timeout", int),
        "MC_READY_TIMEOUT": ("timeouts", "mc_ready_timeout", int),
    }
    for env_key, spec in mapping.items():
        val = os.environ.get(env_key)
        if val is not None:
            section = spec[0]
            key = spec[1]
            convert = spec[2] if len(spec) > 2 else str
            cfg.setdefault(section, {})[key] = convert(val)
    return cfg


CFG = env_override(load_config())

LISTEN_PORT = CFG["watcher"]["listen_port"]
SERVER_MAC = CFG["server"]["mac"]
SERVER_IP = CFG["server"]["ip"]
MC_PORT = CFG["server"]["mc_port"]
SSH_USER = CFG["server"]["ssh_user"]
CONTAINER_NAME = CFG["server"]["container_name"]
WOL_MODE = CFG["wol"]["mode"]
BROADCAST_ADDR = CFG["wol"]["broadcast_address"]
BOOT_TIMEOUT = CFG["timeouts"]["boot_timeout"]
MC_READY_TIMEOUT = CFG["timeouts"]["mc_ready_timeout"]
MAX_PLAYERS = CFG["motd"]["max_players"]

ASSETS_DIR = Path(os.environ.get("MC_WOL_CONFIG", __file__)).resolve().parent / "assets"

def load_motd(filename, fallback):
    path = ASSETS_DIR / filename
    if path.is_file():
        try:
            with open(path, "r", encoding="utf-8") as f:
                content = f.read().strip()
            json.loads(content)
            return content
        except (json.JSONDecodeError, OSError) as e:
            log.warning("Failed to load %s: %s, using fallback", path, e)
    return fallback

def load_icon():
    path = ASSETS_DIR / "server-icon.png"
    if path.is_file():
        try:
            with open(path, "rb") as f:
                data = f.read()
            encoded = base64.b64encode(data).decode("ascii")
            return f"data:image/png;base64,{encoded}"
        except OSError as e:
            log.warning("Failed to load server icon: %s", e)
    return None

MOTD_SLEEPING_FALLBACK = CFG["motd"]["sleeping"]
MOTD_STARTING_FALLBACK = CFG["motd"]["starting"]


def get_motd_sleeping():
    return load_motd("motd-sleeping.json", MOTD_SLEEPING_FALLBACK)


def get_motd_starting():
    return load_motd("motd-starting.json", MOTD_STARTING_FALLBACK)


def get_icon():
    return load_icon()

SSH_KEY_PATH = CFG["server"].get("ssh_key_path") or ""
if not SSH_KEY_PATH:
    if IS_WINDOWS:
        SSH_KEY_PATH = str(Path(os.environ["USERPROFILE"]) / ".ssh" / "id_ed25519")
    else:
        SSH_KEY_PATH = str(Path.home() / ".ssh" / "id_ed25519")

DUCKDNS_ENABLED = CFG.get("duckdns", {}).get("enabled", False)
DUCKDNS_DOMAIN = CFG.get("duckdns", {}).get("domain", "")
DUCKDNS_TOKEN = CFG.get("duckdns", {}).get("token", "")
DUCKDNS_INTERVAL = CFG.get("duckdns", {}).get("update_interval_hours", 6)

def read_varint(data, offset=0):
    result = 0
    shift = 0
    while True:
        if offset >= len(data):
            raise ValueError("Incomplete VarInt")
        b = data[offset]
        offset += 1
        result |= (b & 0x7F) << shift
        if not (b & 0x80):
            break
        shift += 7
        if shift >= 35:
            raise ValueError("VarInt too big")
    return result, offset


def write_varint(value):
    out = bytearray()
    while True:
        b = value & 0x7F
        value >>= 7
        if value:
            b |= 0x80
        out.append(b)
        if not value:
            break
    return bytes(out)


def make_status_response(motd_json, max_players, online=0, icon=None):
    payload = {
        "version": {"name": "", "protocol": -1},
        "players": {"max": max_players, "online": online},
        "description": json.loads(motd_json) if isinstance(motd_json, str) else motd_json,
    }
    if icon:
        payload["favicon"] = icon
    payload_str = json.dumps(payload, ensure_ascii=False)
    payload_bytes = payload_str.encode("utf-8")
    data = write_varint(0) + write_varint(len(payload_bytes)) + payload_bytes
    return write_varint(len(data)) + data


def make_ping_response(payload_long):
    data = write_varint(1) + struct.pack(">q", payload_long)
    return write_varint(len(data)) + data


def parse_handshake(data):
    try:
        pkt_len, off = read_varint(data, 0)
        pkt_id, off = read_varint(data, off)
        if pkt_id != 0x00:
            return None
        _proto_ver, off = read_varint(data, off)
        addr_len, off = read_varint(data, off)
        _server_addr = data[off:off + addr_len].decode("utf-8", errors="replace")
        off += addr_len
        _server_port = struct.unpack(">H", data[off:off + 2])[0]
        off += 2
        next_state, off = read_varint(data, off)
        return next_state, _server_addr, _server_port
    except (ValueError, struct.error, IndexError):
        return None

def mc_port_reachable():
    try:
        s = socket.create_connection((SERVER_IP, MC_PORT), timeout=2)
        s.close()
        return True
    except OSError:
        return False


def mc_accepts_status():
    """Check if MC server responds to a status ping (not just port open)."""
    try:
        s = socket.create_connection((SERVER_IP, MC_PORT), timeout=3)
        # send handshake (status request, state=1)
        host_bytes = SERVER_IP.encode("utf-8")
        handshake_data = (
            write_varint(0x00)
            + write_varint(-1)
            + write_varint(len(host_bytes)) + host_bytes
            + struct.pack(">H", MC_PORT)
            + write_varint(1)
        )
        s.sendall(write_varint(len(handshake_data)) + handshake_data)
        # send status request
        status_req = write_varint(0x00)
        s.sendall(write_varint(len(status_req)) + status_req)
        # try to read response
        s.settimeout(3)
        resp = s.recv(4096)
        s.close()
        return len(resp) > 0
    except OSError:
        return False


def ssh_port_reachable():
    try:
        s = socket.create_connection((SERVER_IP, 22), timeout=2)
        s.close()
        return True
    except OSError:
        return False


def ping_host(ip):
    if IS_WINDOWS:
        cmd = ["ping", "-n", "1", "-w", "1000", ip]
    else:
        cmd = ["ping", "-c", "1", "-W", "1", ip]
    try:
        return subprocess.call(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL) == 0
    except FileNotFoundError:
        return False

def send_magic_packet():
    mac_bytes = bytes.fromhex(SERVER_MAC.replace(":", "").replace("-", ""))
    payload = b"\xff" * 6 + mac_bytes * 16
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        if WOL_MODE == "broadcast":
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_BROADCAST, 1)
            target = (BROADCAST_ADDR, 9)
        else:
            target = (SERVER_IP, 9)
        sock.sendto(payload, target)
        log.info("WoL magic packet sent to %s (%s mode)", target[0], WOL_MODE)
    finally:
        sock.close()

def start_mc_container():
    cmd = [
        "ssh",
        "-o", "StrictHostKeyChecking=no",
        "-o", "ConnectTimeout=10",
        "-i", SSH_KEY_PATH,
        f"{SSH_USER}@{SERVER_IP}",
        "docker", "start", CONTAINER_NAME,
    ]
    log.info("Starting container via SSH: docker start %s", CONTAINER_NAME)
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        if result.returncode == 0:
            log.info("Container started successfully")
            return True
        else:
            log.error("SSH command failed: %s", result.stderr.strip())
            return False
    except subprocess.TimeoutExpired:
        log.error("SSH command timed out")
        return False
    except FileNotFoundError:
        log.error("SSH client not found. Install OpenSSH.")
        return False

boot_lock = asyncio.Lock() if not IS_WINDOWS else None
_booting = False


def _get_boot_lock():
    global boot_lock
    if boot_lock is None:
        boot_lock = asyncio.Lock()
    return boot_lock


async def wait_for_host():
    log.info("Waiting for server PC to respond to ping (timeout %ds)...", BOOT_TIMEOUT)
    loop = asyncio.get_event_loop()
    for _ in range(BOOT_TIMEOUT // 2):
        if await loop.run_in_executor(None, ping_host, SERVER_IP):
            log.info("Server PC is up")
            return True
        await asyncio.sleep(2)
    log.error("Server PC did not respond within %ds", BOOT_TIMEOUT)
    return False


async def wait_for_mc():
    log.info("Waiting for Minecraft port %d (timeout %ds)...", MC_PORT, MC_READY_TIMEOUT)
    loop = asyncio.get_event_loop()
    for _ in range(MC_READY_TIMEOUT // 2):
        if await loop.run_in_executor(None, mc_accepts_status):
            log.info("Minecraft server is ready")
            return True
        await asyncio.sleep(2)
    log.error("Minecraft server not ready within %ds", MC_READY_TIMEOUT)
    return False


async def full_boot_sequence():
    global _booting
    async with _get_boot_lock():
        if mc_port_reachable():
            return True
        _booting = True
        try:
            loop = asyncio.get_event_loop()
            if await loop.run_in_executor(None, ssh_port_reachable):
                log.info("Server PC is up but MC not running, starting container...")
            else:
                log.info("Server PC is sleeping, sending WoL...")
                await loop.run_in_executor(None, send_magic_packet)
                if not await wait_for_host():
                    return False
                # SSH might not be up right after boot
                for _ in range(15):
                    if await loop.run_in_executor(None, ssh_port_reachable):
                        break
                    await asyncio.sleep(2)
                else:
                    log.error("SSH port not reachable after boot")
                    return False
            success = await loop.run_in_executor(None, start_mc_container)
            if not success:
                return False
            return await wait_for_mc()
        finally:
            _booting = False

async def forward(reader, writer):
    try:
        while True:
            data = await reader.read(4096)
            if not data:
                break
            writer.write(data)
            await writer.drain()
    except (ConnectionResetError, BrokenPipeError, OSError):
        pass
    finally:
        try:
            writer.close()
            await writer.wait_closed()
        except OSError:
            pass

async def handle_client(client_reader, client_writer):
    addr = client_writer.get_extra_info("peername")
    try:
        initial_data = await asyncio.wait_for(client_reader.read(4096), timeout=10)
    except (asyncio.TimeoutError, ConnectionResetError):
        client_writer.close()
        return

    if not initial_data:
        client_writer.close()
        return

    handshake = parse_handshake(initial_data)
    if handshake is None:
        client_writer.close()
        return

    next_state, server_addr, server_port = handshake

    if next_state == 1:
        if mc_port_reachable():
            try:
                srv_reader, srv_writer = await asyncio.open_connection(SERVER_IP, MC_PORT)
                srv_writer.write(initial_data)
                await srv_writer.drain()
                await asyncio.gather(
                    forward(client_reader, srv_writer),
                    forward(srv_reader, client_writer),
                )
            except OSError:
                client_writer.close()
            return

        motd = get_motd_starting() if _booting else get_motd_sleeping()
        try:
            status_req = await asyncio.wait_for(client_reader.read(4096), timeout=5)
        except (asyncio.TimeoutError, ConnectionResetError):
            client_writer.close()
            return

        response = make_status_response(motd, MAX_PLAYERS, icon=get_icon())
        client_writer.write(response)
        await client_writer.drain()

        try:
            ping_data = await asyncio.wait_for(client_reader.read(4096), timeout=5)
            if ping_data and len(ping_data) >= 10:
                _, off = read_varint(ping_data, 0)
                _, off = read_varint(ping_data, off)
                payload_long = struct.unpack(">q", ping_data[off:off + 8])[0]
                client_writer.write(make_ping_response(payload_long))
                await client_writer.drain()
        except (asyncio.TimeoutError, ConnectionResetError, struct.error, ValueError):
            pass

        try:
            client_writer.close()
            await client_writer.wait_closed()
        except OSError:
            pass
        return

    if next_state == 2:
        log.info("Login attempt from %s", addr)

        if not mc_port_reachable():
            success = await full_boot_sequence()
            if not success:
                log.error("Boot sequence failed, dropping connection from %s", addr)
                client_writer.close()
                return

        try:
            srv_reader, srv_writer = await asyncio.open_connection(SERVER_IP, MC_PORT)
            log.info("Forwarding connection from %s to %s:%d", addr, SERVER_IP, MC_PORT)
            srv_writer.write(initial_data)
            await srv_writer.drain()
            await asyncio.gather(
                forward(client_reader, srv_writer),
                forward(srv_reader, client_writer),
            )
            log.info("Connection from %s closed", addr)
        except OSError as e:
            log.error("Failed to connect to MC server for %s: %s", addr, e)
            try:
                client_writer.close()
            except OSError:
                pass

async def update_duckdns():
    url = f"https://www.duckdns.org/update?domains={DUCKDNS_DOMAIN}&token={DUCKDNS_TOKEN}&ip="
    loop = asyncio.get_event_loop()
    try:
        response = await loop.run_in_executor(None, lambda: urlopen(url, timeout=10).read().decode())
        if response.strip() == "OK":
            log.info("DuckDNS updated successfully for %s.duckdns.org", DUCKDNS_DOMAIN)
        else:
            log.warning("DuckDNS update returned: %s", response.strip())
    except (URLError, OSError) as e:
        log.warning("DuckDNS update failed: %s", e)


async def duckdns_updater():
    while True:
        await update_duckdns()
        await asyncio.sleep(DUCKDNS_INTERVAL * 3600)

async def main():
    server = await asyncio.start_server(handle_client, "0.0.0.0", LISTEN_PORT)
    log.info("Minecraft Wake-on-Demand Proxy listening on 0.0.0.0:%d", LISTEN_PORT)
    log.info("Server: %s (%s) port %d, container '%s'", SERVER_IP, SERVER_MAC, MC_PORT, CONTAINER_NAME)
    log.info("WoL mode: %s", WOL_MODE)

    tasks = []

    if DUCKDNS_ENABLED:
        log.info("DuckDNS updater enabled for %s.duckdns.org (every %dh)", DUCKDNS_DOMAIN, DUCKDNS_INTERVAL)
        tasks.append(asyncio.create_task(duckdns_updater()))

    shutdown_event = asyncio.Event()

    if not IS_WINDOWS:
        loop = asyncio.get_event_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            loop.add_signal_handler(sig, shutdown_event.set)

    async with server:
        try:
            if IS_WINDOWS:
                await server.serve_forever()
            else:
                serve_task = asyncio.create_task(server.serve_forever())
                await shutdown_event.wait()
                log.info("Shutting down...")
                serve_task.cancel()
                for t in tasks:
                    t.cancel()
        except asyncio.CancelledError:
            pass

    log.info("Proxy stopped")


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except KeyboardInterrupt:
        log.info("Interrupted, exiting")
