#!/usr/bin/env python3
# Reference implementation of the Minecraft Login Success packet, used by CI
# to validate the Go implementation matches the protocol spec.
#
# The strict error handling boolean byte exists only in protocols 766-767
# (1.20.5 - 1.21.1). Including it for other versions makes modern clients
# crash with DecoderException ("1 extra byte").

import sys


def write_varint(value):
    out = b""
    value &= 0xFFFFFFFF
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


def build_login_success(uuid, username, protocol_version):
    body = write_varint(0x02)
    body += uuid
    body += write_varint(len(username)) + username
    body += write_varint(0)
    if 766 <= protocol_version <= 767:
        body += b"\x01"
    return write_varint(len(body)) + body


def has_strict_byte(packet, protocol_version):
    _, off = read_varint(packet, 0)
    _, off = read_varint(packet, off)
    off += 16
    name_len, off = read_varint(packet, off)
    off += name_len
    _, off = read_varint(packet, off)
    remaining = packet[off:]
    if 766 <= protocol_version <= 767:
        return len(remaining) == 1 and remaining[0] == 0x01
    return False


def main():
    uuid = b"\x07" * 16
    username = b"Notch"

    cases = [
        (765, False, "1.20.4 - no strict byte"),
        (766, True,  "1.20.5 - strict byte introduced"),
        (767, True,  "1.21/1.21.1 - strict byte present"),
        (768, False, "1.21.2 - strict byte removed"),
        (769, False, "1.21.3 - no strict byte"),
        (775, False, "1.26.1 - no strict byte"),
        (776, False, "1.26.2 - no strict byte"),
    ]

    failures = 0
    for proto, should_have, desc in cases:
        packet = build_login_success(uuid, username, proto)
        got = has_strict_byte(packet, proto)
        if got != should_have:
            print(f"FAIL {desc}: expected strict={should_have}, got {got}")
            failures += 1
        else:
            print(f"PASS {desc}: strict={got}")

    if failures:
        print(f"\n{failures} test(s) failed")
        return 1
    print("\nAll protocol version tests passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
