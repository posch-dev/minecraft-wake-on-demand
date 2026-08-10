#!/usr/bin/env python3
# Minecraft Wake-on-Demand Proxy

import asyncio
import base64
import ipaddress
import json
import logging
import os
import signal
import socket
import struct
import subprocess
import sys
import time
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
    log.error("Create one with: cp config.example.yml config.yml")
    sys.exit(1)


def env_override(cfg):
    mapping = {
        "SERVER_MAC": ("server", "mac"),
        "SERVER_IP": ("server", "ip"),
        "SERVER_MC_PORT": ("server", "mc_port", int),
        "SERVER_SSH_USER": ("server", "ssh_user"),
        "SERVER_SSH_KEY_PATH": ("server", "ssh_key_path"),
        "SERVER_SSH_STRICT_HOST_KEY": ("server", "ssh_strict_host_key"),
        "SERVER_SSH_KNOWN_HOSTS": ("server", "ssh_known_hosts"),
        "SERVER_CONTAINER_NAME": ("server", "container_name"),
        "WATCHER_LISTEN_ADDRESS": ("watcher", "listen_address"),
        "WATCHER_LISTEN_PORT": ("watcher", "listen_port", int),
        "WOL_MODE": ("wol", "mode"),
        "WOL_BROADCAST_ADDRESS": ("wol", "broadcast_address"),
        "DUCKDNS_ENABLED": ("duckdns", "enabled", lambda v: v.lower() == "true"),
        "DUCKDNS_DOMAIN": ("duckdns", "domain"),
        "DUCKDNS_TOKEN": ("duckdns", "token"),
        "DUCKDNS_UPDATE_INTERVAL_HOURS": ("duckdns", "update_interval_hours", int),
        "BOOT_TIMEOUT": ("timeouts", "boot_timeout", int),
        "MC_READY_TIMEOUT": ("timeouts", "mc_ready_timeout", int),
        "BOOT_COOLDOWN": ("limits", "boot_cooldown", int),
        "BOOT_FAILURE_BACKOFF": ("limits", "boot_failure_backoff", int),
        "BOOT_MAX_BACKOFF": ("limits", "boot_max_backoff", int),
        "TRANSFER_ENABLED": ("transfer", "enabled", lambda v: v.lower() == "true"),
        "TRANSFER_HOST": ("transfer", "host"),
        "TRANSFER_PORT": ("transfer", "port", int),
        "TRANSFER_LOCAL_NETWORKS": ("transfer", "local_networks"),
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

LISTEN_ADDRESS = CFG["watcher"].get("listen_address") or "0.0.0.0"
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

_limits = CFG.get("limits") or {}
BOOT_COOLDOWN = _limits.get("boot_cooldown", 10)
BOOT_FAILURE_BACKOFF = _limits.get("boot_failure_backoff", 60)
BOOT_MAX_BACKOFF = _limits.get("boot_max_backoff", 900)

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
MOTD_LOGIN_WAIT = CFG["motd"].get(
    "login_wait",
    '{"text":"Server is waking up. Please reconnect in a moment.","color":"gold"}',
)


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

SSH_STRICT_MODES = ("accept-new", "yes", "no")
SSH_STRICT_HOST_KEY = str(
    CFG["server"].get("ssh_strict_host_key") or "accept-new"
).lower()
if SSH_STRICT_HOST_KEY not in SSH_STRICT_MODES:
    log.warning(
        "Invalid server.ssh_strict_host_key %r, falling back to 'accept-new'",
        SSH_STRICT_HOST_KEY,
    )
    SSH_STRICT_HOST_KEY = "accept-new"
if SSH_STRICT_HOST_KEY == "no":
    log.warning(
        "server.ssh_strict_host_key is 'no', any host key is accepted, "
        "which allows man-in-the-middle attacks on the SSH connection"
    )

SSH_KNOWN_HOSTS = CFG["server"].get("ssh_known_hosts") or ""

DUCKDNS_ENABLED = CFG.get("duckdns", {}).get("enabled", False)
DUCKDNS_DOMAIN = CFG.get("duckdns", {}).get("domain", "")
DUCKDNS_TOKEN = CFG.get("duckdns", {}).get("token", "")
DUCKDNS_INTERVAL = CFG.get("duckdns", {}).get("update_interval_hours", 6)

TRANSFER_ENABLED = CFG.get("transfer", {}).get("enabled", False)
TRANSFER_HOST = CFG.get("transfer", {}).get("host", "")
TRANSFER_PORT = CFG.get("transfer", {}).get("port", 25566)


def parse_networks(value):
    if isinstance(value, str):
        value = [p for p in value.replace(",", " ").split() if p]
    networks = []
    for entry in value or []:
        try:
            networks.append(ipaddress.ip_network(str(entry), strict=False))
        except ValueError:
            log.warning("Ignoring invalid network in transfer.local_networks: %s", entry)
    return networks


TRANSFER_LOCAL_NETWORKS = parse_networks(CFG.get("transfer", {}).get("local_networks"))


def is_local_client(addr):
    # Players on the LAN cannot reach the public host unless the router does
    # NAT loopback, so they are sent to the server directly instead.
    if not addr:
        return False
    try:
        ip = ipaddress.ip_address(addr[0])
    except ValueError:
        return False
    if ip.version == 6 and ip.ipv4_mapped:
        ip = ip.ipv4_mapped
    if TRANSFER_LOCAL_NETWORKS:
        return any(ip in net for net in TRANSFER_LOCAL_NETWORKS)
    return ip.is_private or ip.is_loopback

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


MAX_USERNAME_LEN = 16


def sanitize_for_log(value, max_len=64):
    # Client-supplied text reaches the log, so newlines could forge log lines.
    return "".join(c if c.isprintable() else "?" for c in str(value)[:max_len])


def parse_login_start(data, offset=0):
    try:
        pkt_len, off = read_varint(data, offset)
        pkt_id, off = read_varint(data, off)
        if pkt_id != 0x00:
            return None, None
        name_len, off = read_varint(data, off)
        if not 0 < name_len <= MAX_USERNAME_LEN * 4:  # UTF-8 worst case
            return None, None
        name_bytes = data[off:off + name_len]
        if len(name_bytes) != name_len:
            return None, None
        name = name_bytes.decode("utf-8")
        if len(name) > MAX_USERNAME_LEN:
            return None, None
        off += name_len
        uuid_bytes = data[off:off + 16]
        if len(uuid_bytes) != 16:
            return None, None
        return name, uuid_bytes
    except (ValueError, IndexError, UnicodeDecodeError):
        return None, None


def make_login_success(uuid_bytes, username):
    username_bytes = username.encode("utf-8")
    data = (
        write_varint(0x02)
        + uuid_bytes
        + write_varint(len(username_bytes)) + username_bytes
        + write_varint(0)  # no properties
        + bytes([0x01])  # strict error handling
    )
    return write_varint(len(data)) + data


def make_login_disconnect(reason_json):
    # Login-state disconnect, so the client shows a message instead of an error.
    reason_bytes = reason_json.encode("utf-8")
    data = write_varint(0x00) + write_varint(len(reason_bytes)) + reason_bytes
    return write_varint(len(data)) + data


async def send_login_disconnect(writer, reason_json):
    try:
        writer.write(make_login_disconnect(reason_json))
        await writer.drain()
    except (OSError, ConnectionResetError):
        pass
    try:
        writer.close()
        await writer.wait_closed()
    except OSError:
        pass


def make_transfer_packet(host, port):
    host_bytes = host.encode("utf-8")
    data = (
        write_varint(0x0B)  # Transfer packet ID (configuration state)
        + write_varint(len(host_bytes)) + host_bytes
        + write_varint(port)
    )
    return write_varint(len(data)) + data


def parse_handshake(data):
    try:
        pkt_len, off = read_varint(data, 0)
        pkt_end = off + pkt_len
        pkt_id, off = read_varint(data, off)
        if pkt_id != 0x00:
            return None
        proto_ver, off = read_varint(data, off)
        addr_len, off = read_varint(data, off)
        server_addr = data[off:off + addr_len].decode("utf-8", errors="replace")
        off += addr_len
        server_port = struct.unpack(">H", data[off:off + 2])[0]
        off += 2
        next_state, off = read_varint(data, off)
        return next_state, server_addr, server_port, proto_ver, pkt_end
    except (ValueError, struct.error, IndexError):
        return None

MC_REACHABLE_TTL = 2.0

_mc_reachable = {"value": False, "checked_at": 0.0}
_mc_reachable_lock = None


def _get_mc_reachable_lock():
    global _mc_reachable_lock
    if _mc_reachable_lock is None:
        _mc_reachable_lock = asyncio.Lock()
    return _mc_reachable_lock


async def mc_port_reachable(force=False):
    # Never block the event loop here: a burst of connections shares one probe.
    if not force and time.monotonic() - _mc_reachable["checked_at"] < MC_REACHABLE_TTL:
        return _mc_reachable["value"]

    async with _get_mc_reachable_lock():
        # Another coroutine may have refreshed the cache while we waited.
        if not force and time.monotonic() - _mc_reachable["checked_at"] < MC_REACHABLE_TTL:
            return _mc_reachable["value"]
        try:
            _, writer = await asyncio.wait_for(
                asyncio.open_connection(SERVER_IP, MC_PORT), timeout=2
            )
            try:
                writer.close()
                await writer.wait_closed()
            except OSError:
                pass
            value = True
        except (OSError, asyncio.TimeoutError):
            value = False
        _mc_reachable["value"] = value
        _mc_reachable["checked_at"] = time.monotonic()
        return value


def mc_accepts_status():
    # An open port is not enough, the container may still be booting.
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
    cmd = ["ssh", "-o", f"StrictHostKeyChecking={SSH_STRICT_HOST_KEY}"]
    if SSH_KNOWN_HOSTS:
        cmd += ["-o", f"UserKnownHostsFile={SSH_KNOWN_HOSTS}"]
    cmd += [
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
_last_boot_attempt = 0.0
_boot_failures = 0


def boot_cooldown_remaining():
    # Seconds until the next wake attempt is allowed, 0 when one may run now.
    if _last_boot_attempt == 0.0:
        return 0.0
    if _boot_failures:
        delay = min(
            BOOT_FAILURE_BACKOFF * (2 ** (_boot_failures - 1)), BOOT_MAX_BACKOFF
        )
    else:
        delay = BOOT_COOLDOWN
    return max(0.0, delay - (time.monotonic() - _last_boot_attempt))


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
    global _booting, _last_boot_attempt, _boot_failures
    async with _get_boot_lock():
        # Another coroutine may have finished a boot while we waited for the
        # lock, so this probe must not come from the cache.
        if await mc_port_reachable(force=True):
            return True

        cooldown = boot_cooldown_remaining()
        if cooldown > 0:
            log.warning(
                "Boot attempt refused, %ds cooldown remaining (%d consecutive failures)",
                int(cooldown),
                _boot_failures,
            )
            return False

        _last_boot_attempt = time.monotonic()
        _booting = True
        ok = False
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
            ok = await wait_for_mc()
            if ok:
                _mc_reachable["value"] = True
                _mc_reachable["checked_at"] = time.monotonic()
            return ok
        finally:
            _booting = False
            if ok:
                _boot_failures = 0
            else:
                _boot_failures += 1
                log.warning(
                    "Boot sequence failed (%d in a row), next attempt in %ds",
                    _boot_failures,
                    int(boot_cooldown_remaining()),
                )

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

    next_state, server_addr, server_port, proto_ver, handshake_end = handshake

    if next_state == 1:
        if await mc_port_reachable():
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

        # Clients are free to pack handshake, status request and ping into one
        # segment, so only read again when nothing followed the handshake.
        rest = initial_data[handshake_end:]
        if not rest:
            try:
                rest = await asyncio.wait_for(client_reader.read(4096), timeout=5)
            except (asyncio.TimeoutError, ConnectionResetError):
                client_writer.close()
                return
        if not rest:
            client_writer.close()
            return

        try:
            req_len, off = read_varint(rest, 0)
            ping_data = rest[off + req_len:]
        except ValueError:
            ping_data = b""

        response = make_status_response(motd, MAX_PLAYERS, icon=get_icon())
        client_writer.write(response)
        await client_writer.drain()

        try:
            if not ping_data:
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

        if not await mc_port_reachable():
            success = await full_boot_sequence()
            if not success:
                log.info("Server not ready for %s, sending wait message", addr)
                await send_login_disconnect(client_writer, MOTD_LOGIN_WAIT)
                return

        if TRANSFER_ENABLED:
            try:
                login_data = initial_data[handshake_end:]
                if not login_data:
                    login_data = await asyncio.wait_for(client_reader.read(4096), timeout=10)

                name, uuid_bytes = parse_login_start(login_data)
                if name is None:
                    log.warning("Failed to parse login start from %s", addr)
                    client_writer.close()
                    return

                client_writer.write(make_login_success(uuid_bytes, name))
                await client_writer.drain()

                await asyncio.wait_for(client_reader.read(4096), timeout=5)

                if is_local_client(addr):
                    target_host, target_port = SERVER_IP, MC_PORT
                else:
                    target_host, target_port = TRANSFER_HOST, TRANSFER_PORT

                log.info(
                    "Transferring %s to %s:%d",
                    sanitize_for_log(name),
                    target_host,
                    target_port,
                )
                client_writer.write(make_transfer_packet(target_host, target_port))
                await client_writer.drain()

                try:
                    client_writer.close()
                    await client_writer.wait_closed()
                except OSError:
                    pass
            except (asyncio.TimeoutError, ConnectionResetError, OSError) as e:
                log.error("Transfer failed for %s: %s", addr, e)
                try:
                    client_writer.close()
                except OSError:
                    pass
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
    server = await asyncio.start_server(handle_client, LISTEN_ADDRESS, LISTEN_PORT)
    log.info(
        "Minecraft Wake-on-Demand Proxy listening on %s:%d", LISTEN_ADDRESS, LISTEN_PORT
    )
    log.info("Server: %s (%s) port %d, container '%s'", SERVER_IP, SERVER_MAC, MC_PORT, CONTAINER_NAME)
    log.info("WoL mode: %s", WOL_MODE)
    if TRANSFER_ENABLED:
        log.info("Transfer mode: %s:%d", TRANSFER_HOST, TRANSFER_PORT)
        log.info(
            "Local players are transferred to %s:%d instead (local networks: %s)",
            SERVER_IP,
            MC_PORT,
            ", ".join(str(n) for n in TRANSFER_LOCAL_NETWORKS) or "any private IP",
        )
    else:
        log.info("Proxy mode: full connection forwarding")

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
