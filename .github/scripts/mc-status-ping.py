#!/usr/bin/env python3
# Sends a Minecraft status request and prints the description the server
# answered with, one rendered line per output line.
import json
import socket
import struct
import sys


def write_varint(value):
    out = b""
    while True:
        byte = value & 0x7F
        value >>= 7
        if value:
            byte |= 0x80
        out += bytes([byte])
        if not value:
            break
    return out


def read_varint(data, offset):
    result = 0
    shift = 0
    while True:
        byte = data[offset]
        offset += 1
        result |= (byte & 0x7F) << shift
        if not byte & 0x80:
            break
        shift += 7
    return result, offset


def status(host, port, timeout=10):
    handshake = (
        write_varint(0x00)
        + write_varint(770)
        + write_varint(len(host.encode())) + host.encode()
        + struct.pack(">H", port)
        + write_varint(1)
    )
    request = write_varint(0x00)

    with socket.create_connection((host, port), timeout=timeout) as sock:
        sock.sendall(write_varint(len(handshake)) + handshake)
        sock.sendall(write_varint(len(request)) + request)
        sock.settimeout(timeout)

        data = b""
        while True:
            chunk = sock.recv(4096)
            if not chunk:
                break
            data += chunk
            try:
                length, offset = read_varint(data, 0)
            except IndexError:
                continue
            if len(data) >= offset + length:
                break

    _, offset = read_varint(data, 0)
    packet_id, offset = read_varint(data, offset)
    if packet_id != 0x00:
        raise SystemExit("unexpected packet id %d" % packet_id)
    length, offset = read_varint(data, offset)
    return json.loads(data[offset:offset + length].decode("utf-8"))


def render(description):
    if isinstance(description, str):
        return description
    text = description.get("text", "")
    for extra in description.get("extra", []):
        text += extra.get("text", "")
    return text


if __name__ == "__main__":
    if len(sys.argv) != 3:
        raise SystemExit("usage: mc-status-ping.py <host> <port>")
    payload = status(sys.argv[1], int(sys.argv[2]))
    print("players: %d/%d" % (payload["players"]["online"], payload["players"]["max"]),
          file=sys.stderr)
    for line in render(payload["description"]).split("\n"):
        print(line)
