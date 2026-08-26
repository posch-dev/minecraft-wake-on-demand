package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestVarIntRoundTrip(t *testing.T) {
	values := []int32{0, 1, 2, 127, 128, 255, 2147483647, -1, -2147483648}
	for _, want := range values {
		encoded := writeVarInt(want)
		got, off, err := readVarInt(encoded, 0)
		if err != nil {
			t.Fatalf("%d: %v", want, err)
		}
		if got != want {
			t.Errorf("%d round tripped to %d", want, got)
		}
		if off != len(encoded) {
			t.Errorf("%d consumed %d of %d bytes", want, off, len(encoded))
		}
	}
}

// The Python version shifted a negative int and looped until it ran out of
// memory, which hung the readiness probe in wait_for_mc.
func TestNegativeVarIntTerminates(t *testing.T) {
	got := writeVarInt(-1)
	want := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	if !bytes.Equal(got, want) {
		t.Errorf("writeVarInt(-1) = % x, want % x", got, want)
	}
	if len(writeVarInt(-2147483648)) != 5 {
		t.Error("every negative VarInt has to fit in five bytes")
	}
}

func TestVarIntKnownEncodings(t *testing.T) {
	cases := map[int32][]byte{
		0:     {0x00},
		1:     {0x01},
		127:   {0x7F},
		128:   {0x80, 0x01},
		255:   {0xFF, 0x01},
		25565: {0xDD, 0xC7, 0x01},
	}
	for value, want := range cases {
		if got := writeVarInt(value); !bytes.Equal(got, want) {
			t.Errorf("writeVarInt(%d) = % x, want % x", value, got, want)
		}
	}
}

func TestVarIntErrors(t *testing.T) {
	if _, _, err := readVarInt(nil, 0); err != ErrIncompleteVarInt {
		t.Errorf("empty input gave %v", err)
	}
	if _, _, err := readVarInt([]byte{0x80}, 0); err != ErrIncompleteVarInt {
		t.Errorf("truncated input gave %v", err)
	}
	tooLong := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
	if _, _, err := readVarInt(tooLong, 0); err != ErrVarIntTooBig {
		t.Errorf("oversized input gave %v", err)
	}
}

func buildHandshake(proto int32, addr string, port uint16, next int32) []byte {
	body := writeVarInt(0x00)
	body = append(body, writeVarInt(proto)...)
	body = append(body, writeString(addr)...)
	body = binary.BigEndian.AppendUint16(body, port)
	body = append(body, writeVarInt(next)...)
	return framePacket(body)
}

func TestParseHandshake(t *testing.T) {
	for _, next := range []int32{1, 2} {
		raw := buildHandshake(770, "mc.example.org", 25565, next)
		hs, err := parseHandshake(raw)
		if err != nil {
			t.Fatalf("next=%d: %v", next, err)
		}
		if hs.NextState != next {
			t.Errorf("next state = %d, want %d", hs.NextState, next)
		}
		if hs.ServerAddress != "mc.example.org" {
			t.Errorf("address = %q", hs.ServerAddress)
		}
		if hs.ServerPort != 25565 {
			t.Errorf("port = %d", hs.ServerPort)
		}
		if hs.ProtocolVersion != 770 {
			t.Errorf("protocol = %d", hs.ProtocolVersion)
		}
		if hs.End != len(raw) {
			t.Errorf("End = %d, want %d", hs.End, len(raw))
		}
	}
}

// Clients may pack the handshake and the status request into one segment, so
// End has to point at the start of the trailing data.
func TestParseHandshakeWithTrailingData(t *testing.T) {
	raw := buildHandshake(770, "host", 25565, 1)
	trailer := makeStatusRequest()
	combined := append(append([]byte{}, raw...), trailer...)

	hs, err := parseHandshake(combined)
	if err != nil {
		t.Fatal(err)
	}
	if hs.End != len(raw) {
		t.Fatalf("End = %d, want %d", hs.End, len(raw))
	}
	if !bytes.Equal(combined[hs.End:], trailer) {
		t.Error("trailing bytes did not survive")
	}
}

func TestParseHandshakeRejectsGarbage(t *testing.T) {
	cases := map[string][]byte{
		"empty":         {},
		"truncated":     {0x10, 0x00},
		"wrong id":      framePacket(append(writeVarInt(0x42), writeVarInt(1)...)),
		"address lies":  framePacket(append(append(writeVarInt(0x00), writeVarInt(770)...), writeVarInt(200)...)),
		"no next state": framePacket(append(append(writeVarInt(0x00), writeVarInt(770)...), writeString("h")...)),
	}
	for name, raw := range cases {
		if _, err := parseHandshake(raw); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestStatusResponseDecodes(t *testing.T) {
	motd := "{\"text\":\"sleeping\",\"color\":\"yellow\"}"
	raw, err := makeStatusResponse(motd, 10, 0, "data:image/png;base64,AAA", "1.21.4", 769)
	if err != nil {
		t.Fatal(err)
	}

	length, off, err := readVarInt(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if int(length) != len(raw)-off {
		t.Errorf("frame length %d does not match body %d", length, len(raw)-off)
	}
	pktID, off, err := readVarInt(raw, off)
	if err != nil || pktID != 0x00 {
		t.Fatalf("packet id = %d, err %v", pktID, err)
	}
	strLen, off, err := readVarInt(raw, off)
	if err != nil {
		t.Fatal(err)
	}
	body := raw[off : off+int(strLen)]

	var decoded statusPayload
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if decoded.Players.Max != 10 || decoded.Players.Online != 0 {
		t.Errorf("players = %+v", decoded.Players)
	}
	if decoded.Version.Protocol != 769 {
		t.Errorf("protocol = %d, want 769", decoded.Version.Protocol)
	}
	if decoded.Version.Name != "1.21.4" {
		t.Errorf("version name = %q, want 1.21.4", decoded.Version.Name)
	}
	if string(decoded.Description) != motd {
		t.Errorf("description = %s", decoded.Description)
	}
	if decoded.Favicon == "" {
		t.Error("favicon was dropped")
	}
}

func TestStatusResponseOmitsEmptyFavicon(t *testing.T) {
	raw, err := makeStatusResponse("{\"text\":\"x\"}", 5, 0, "", "", 770)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("favicon")) {
		t.Error("favicon key should be absent when there is no icon")
	}
}

func TestPingResponse(t *testing.T) {
	raw := makePingResponse(0x0123456789ABCDEF)
	_, off, err := readVarInt(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	pktID, off, err := readVarInt(raw, off)
	if err != nil || pktID != 0x01 {
		t.Fatalf("packet id = %d", pktID)
	}
	if got := binary.BigEndian.Uint64(raw[off:]); got != 0x0123456789ABCDEF {
		t.Errorf("payload = %x", got)
	}
}

func buildLoginStart(name string, uuid []byte) []byte {
	body := append(writeVarInt(0x00), writeString(name)...)
	body = append(body, uuid...)
	return framePacket(body)
}

func TestParseLoginStart(t *testing.T) {
	uuid := bytes.Repeat([]byte{0xAB}, 16)
	name, got, err := parseLoginStart(buildLoginStart("Notch", uuid))
	if err != nil {
		t.Fatal(err)
	}
	if name != "Notch" {
		t.Errorf("name = %q", name)
	}
	if !bytes.Equal(got, uuid) {
		t.Errorf("uuid = % x", got)
	}
}

func TestParseLoginStartRejectsBadInput(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x01}, 16)
	cases := map[string][]byte{
		"empty":         {},
		"name too long": buildLoginStart("ThisNameIsWayTooLongForMinecraft", uuid),
		"no uuid":       framePacket(append(writeVarInt(0x00), writeString("Notch")...)),
		"empty name":    buildLoginStart("", uuid),
		"wrong id":      framePacket(append(writeVarInt(0x05), writeString("Notch")...)),
	}
	for name, raw := range cases {
		if _, _, err := parseLoginStart(raw); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTransferPacket(t *testing.T) {
	raw := makeTransferPacket("mc.example.org", 25566)
	_, off, err := readVarInt(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	pktID, off, err := readVarInt(raw, off)
	if err != nil || pktID != 0x0B {
		t.Fatalf("packet id = %d, want 0x0B", pktID)
	}
	hostLen, off, err := readVarInt(raw, off)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[off:off+int(hostLen)]) != "mc.example.org" {
		t.Errorf("host = %q", raw[off:off+int(hostLen)])
	}
	port, _, err := readVarInt(raw, off+int(hostLen))
	if err != nil || port != 25566 {
		t.Errorf("port = %d", port)
	}
}

func TestLoginSuccessAndDisconnect(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := makeLoginSuccess(uuid, "Notch", 767)
	_, off, _ := readVarInt(raw, 0)
	pktID, off, err := readVarInt(raw, off)
	if err != nil || pktID != 0x02 {
		t.Fatalf("login success id = %d", pktID)
	}
	if !bytes.Equal(raw[off:off+16], uuid) {
		t.Error("uuid did not survive")
	}

	reason := "{\"text\":\"wait\"}"
	dis := makeLoginDisconnect(reason)
	_, off, _ = readVarInt(dis, 0)
	pktID, off, err = readVarInt(dis, off)
	if err != nil || pktID != 0x00 {
		t.Fatalf("disconnect id = %d", pktID)
	}
	strLen, off, _ := readVarInt(dis, off)
	if string(dis[off:off+int(strLen)]) != reason {
		t.Errorf("reason = %q", dis[off:off+int(strLen)])
	}
}

// Protocol 767 (1.21.1) must include the strict error handling byte.
func TestLoginSuccessProtocol767IncludesStrictByte(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := makeLoginSuccess(uuid, "Notch", 767)

	// Parse past frame, packet ID, UUID, username, properties count.
	_, off, _ := readVarInt(raw, 0)
	_, off, _ = readVarInt(raw, off) // packet ID
	off += 16                        // UUID
	nameLen, off, _ := readVarInt(raw, off)
	off += int(nameLen)         // username bytes
	_, off, _ = readVarInt(raw, off) // properties count (0)

	if off >= len(raw) {
		t.Fatal("packet too short, strict error handling byte is missing")
	}
	if raw[off] != 0x01 {
		t.Errorf("strict error handling byte = %x, want 0x01", raw[off])
	}
	// Should be the last byte of the body.
	if off+1 != len(raw) {
		t.Errorf("unexpected trailing bytes after strict error handling")
	}
}

// Protocol 766 (1.20.5) must also include the strict error handling byte.
func TestLoginSuccessProtocol766IncludesStrictByte(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := makeLoginSuccess(uuid, "Notch", 766)

	_, off, _ := readVarInt(raw, 0)
	_, off, _ = readVarInt(raw, off)
	off += 16
	nameLen, off, _ := readVarInt(raw, off)
	off += int(nameLen)
	_, off, _ = readVarInt(raw, off)

	if off >= len(raw) {
		t.Fatal("packet too short, strict error handling byte is missing")
	}
	if raw[off] != 0x01 {
		t.Errorf("strict error handling byte = %x, want 0x01", raw[off])
	}
}

// Protocol 768 (1.21.2) must NOT include the strict error handling byte.
func TestLoginSuccessProtocol768OmitsStrictByte(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := makeLoginSuccess(uuid, "Notch", 768)

	_, off, _ := readVarInt(raw, 0)
	_, off, _ = readVarInt(raw, off)
	off += 16
	nameLen, off, _ := readVarInt(raw, off)
	off += int(nameLen)
	_, off, _ = readVarInt(raw, off)

	if off != len(raw) {
		t.Errorf("packet has %d bytes after properties, want 0 (no strict byte)", len(raw)-off)
	}
}

// Protocol 776 (26.x) must NOT include the strict error handling byte.
func TestLoginSuccessProtocol776OmitsStrictByte(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := makeLoginSuccess(uuid, "Notch", 776)

	_, off, _ := readVarInt(raw, 0)
	_, off, _ = readVarInt(raw, off)
	off += 16
	nameLen, off, _ := readVarInt(raw, off)
	off += int(nameLen)
	_, off, _ = readVarInt(raw, off)

	if off != len(raw) {
		t.Errorf("packet has %d bytes after properties, want 0 (no strict byte)", len(raw)-off)
	}
}

// A status handshake carries protocol version -1, the case that used to hang.
func TestStatusHandshakeIsWellFormed(t *testing.T) {
	raw := makeStatusHandshake("192.168.1.100", 25565)
	hs, err := parseHandshake(raw)
	if err != nil {
		t.Fatal(err)
	}
	if hs.ProtocolVersion != -1 {
		t.Errorf("protocol = %d, want -1", hs.ProtocolVersion)
	}
	if hs.NextState != 1 {
		t.Errorf("next state = %d, want 1", hs.NextState)
	}
	if hs.ServerAddress != "192.168.1.100" {
		t.Errorf("address = %q", hs.ServerAddress)
	}
}

func TestStatusResponseEchoesClientProtocol(t *testing.T) {
	raw, err := makeStatusResponse("{\"text\":\"sleeping\"}", 10, 0, "", "", 770)
	if err != nil {
		t.Fatal(err)
	}
	_, off, _ := readVarInt(raw, 0)
	_, off, _ = readVarInt(raw, off)
	strLen, off, _ := readVarInt(raw, off)
	var decoded statusPayload
	if err := json.Unmarshal(raw[off:off+int(strLen)], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version.Protocol != 770 {
		t.Errorf("protocol = %d, want 770", decoded.Version.Protocol)
	}
}
