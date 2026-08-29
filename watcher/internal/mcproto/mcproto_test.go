package mcproto

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/posch-dev/minecraft-wake-on-demand/watcher/internal/config"
)

func TestVarIntRoundTrip(t *testing.T) {
	values := []int32{0, 1, 2, 127, 128, 255, 2147483647, -1, -2147483648}
	for _, want := range values {
		encoded := WriteVarInt(want)
		got, off, err := ReadVarInt(encoded, 0)
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
	got := WriteVarInt(-1)
	want := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}
	if !bytes.Equal(got, want) {
		t.Errorf("writeVarInt(-1) = % x, want % x", got, want)
	}
	if len(WriteVarInt(-2147483648)) != 5 {
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
		if got := WriteVarInt(value); !bytes.Equal(got, want) {
			t.Errorf("writeVarInt(%d) = % x, want % x", value, got, want)
		}
	}
}

func TestVarIntErrors(t *testing.T) {
	if _, _, err := ReadVarInt(nil, 0); err != ErrIncompleteVarInt {
		t.Errorf("empty input gave %v", err)
	}
	if _, _, err := ReadVarInt([]byte{0x80}, 0); err != ErrIncompleteVarInt {
		t.Errorf("truncated input gave %v", err)
	}
	tooLong := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80}
	if _, _, err := ReadVarInt(tooLong, 0); err != ErrVarIntTooBig {
		t.Errorf("oversized input gave %v", err)
	}
}

func TestParseHandshake(t *testing.T) {
	for _, next := range []int32{1, 2} {
		raw := MakeHandshake(770, "mc.example.org", 25565, next)
		hs, err := ParseHandshake(raw)
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
	raw := MakeHandshake(770, "host", 25565, 1)
	trailer := MakeStatusRequest()
	combined := append(append([]byte{}, raw...), trailer...)

	hs, err := ParseHandshake(combined)
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
		"wrong id":      FramePacket(append(WriteVarInt(0x42), WriteVarInt(1)...)),
		"address lies":  FramePacket(append(append(WriteVarInt(0x00), WriteVarInt(770)...), WriteVarInt(200)...)),
		"no next state": FramePacket(append(append(WriteVarInt(0x00), WriteVarInt(770)...), WriteString("h")...)),
	}
	for name, raw := range cases {
		if _, err := ParseHandshake(raw); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestStatusResponseDecodes(t *testing.T) {
	motd := "{\"text\":\"sleeping\",\"color\":\"yellow\"}"
	raw, err := MakeStatusResponse(motd, 10, 0, "data:image/png;base64,AAA", "1.21.4", 769)
	if err != nil {
		t.Fatal(err)
	}

	length, off, err := ReadVarInt(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if int(length) != len(raw)-off {
		t.Errorf("frame length %d does not match body %d", length, len(raw)-off)
	}
	pktID, off, err := ReadVarInt(raw, off)
	if err != nil || pktID != 0x00 {
		t.Fatalf("packet id = %d, err %v", pktID, err)
	}
	strLen, off, err := ReadVarInt(raw, off)
	if err != nil {
		t.Fatal(err)
	}
	body := raw[off : off+int(strLen)]

	var decoded StatusPayload
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
	raw, err := MakeStatusResponse("{\"text\":\"x\"}", 5, 0, "", "", 770)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("favicon")) {
		t.Error("favicon key should be absent when there is no icon")
	}
}

func TestPingResponse(t *testing.T) {
	raw := MakePingResponse(0x0123456789ABCDEF)
	_, off, err := ReadVarInt(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	pktID, off, err := ReadVarInt(raw, off)
	if err != nil || pktID != 0x01 {
		t.Fatalf("packet id = %d", pktID)
	}
	if got := binary.BigEndian.Uint64(raw[off:]); got != 0x0123456789ABCDEF {
		t.Errorf("payload = %x", got)
	}
}

func TestParseLoginStart(t *testing.T) {
	uuid := bytes.Repeat([]byte{0xAB}, 16)
	name, got, err := ParseLoginStart(MakeLoginStart("Notch", uuid))
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
		"name too long": MakeLoginStart("ThisNameIsWayTooLongForMinecraft", uuid),
		"no uuid":       FramePacket(append(WriteVarInt(0x00), WriteString("Notch")...)),
		"empty name":    MakeLoginStart("", uuid),
		"wrong id":      FramePacket(append(WriteVarInt(0x05), WriteString("Notch")...)),
	}
	for name, raw := range cases {
		if _, _, err := ParseLoginStart(raw); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTransferPacket(t *testing.T) {
	raw := MakeTransferPacket("mc.example.org", 25566)
	_, off, err := ReadVarInt(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	pktID, off, err := ReadVarInt(raw, off)
	if err != nil || pktID != 0x0B {
		t.Fatalf("packet id = %d, want 0x0B", pktID)
	}
	hostLen, off, err := ReadVarInt(raw, off)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw[off:off+int(hostLen)]) != "mc.example.org" {
		t.Errorf("host = %q", raw[off:off+int(hostLen)])
	}
	port, _, err := ReadVarInt(raw, off+int(hostLen))
	if err != nil || port != 25566 {
		t.Errorf("port = %d", port)
	}
}

func TestLoginSuccessAndDisconnect(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := MakeLoginSuccess(uuid, "Notch", 767)
	_, off, _ := ReadVarInt(raw, 0)
	pktID, off, err := ReadVarInt(raw, off)
	if err != nil || pktID != 0x02 {
		t.Fatalf("login success id = %d", pktID)
	}
	if !bytes.Equal(raw[off:off+16], uuid) {
		t.Error("uuid did not survive")
	}

	reason := "{\"text\":\"wait\"}"
	dis := MakeLoginDisconnect(reason)
	_, off, _ = ReadVarInt(dis, 0)
	pktID, off, err = ReadVarInt(dis, off)
	if err != nil || pktID != 0x00 {
		t.Fatalf("disconnect id = %d", pktID)
	}
	strLen, off, _ := ReadVarInt(dis, off)
	if string(dis[off:off+int(strLen)]) != reason {
		t.Errorf("reason = %q", dis[off:off+int(strLen)])
	}
}

// Protocol 767 (1.21.1) must include the strict error handling byte.
func TestLoginSuccessProtocol767IncludesStrictByte(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := MakeLoginSuccess(uuid, "Notch", 767)

	// Parse past frame, packet ID, UUID, username, properties count.
	_, off, _ := ReadVarInt(raw, 0)
	_, off, _ = ReadVarInt(raw, off)
	off += 16
	nameLen, off, _ := ReadVarInt(raw, off)
	off += int(nameLen)
	_, off, _ = ReadVarInt(raw, off)

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
	raw := MakeLoginSuccess(uuid, "Notch", 766)

	_, off, _ := ReadVarInt(raw, 0)
	_, off, _ = ReadVarInt(raw, off)
	off += 16
	nameLen, off, _ := ReadVarInt(raw, off)
	off += int(nameLen)
	_, off, _ = ReadVarInt(raw, off)

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
	raw := MakeLoginSuccess(uuid, "Notch", 768)

	_, off, _ := ReadVarInt(raw, 0)
	_, off, _ = ReadVarInt(raw, off)
	off += 16
	nameLen, off, _ := ReadVarInt(raw, off)
	off += int(nameLen)
	_, off, _ = ReadVarInt(raw, off)

	if off != len(raw) {
		t.Errorf("packet has %d bytes after properties, want 0 (no strict byte)", len(raw)-off)
	}
}

// Protocol 770 (1.21.5) sits between the two trailing fields and gets neither.
func TestLoginSuccessProtocol770OmitsBothTrailingFields(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := MakeLoginSuccess(uuid, "Notch", 770)

	off := afterLoginSuccessProperties(t, raw)
	if off != len(raw) {
		t.Errorf("packet has %d bytes after properties, want 0", len(raw)-off)
	}
}

// Protocol 776 (26.2) ends in a session id, and leaving it out is what made the
// client refuse the packet with a decoder error.
func TestLoginSuccessProtocol776CarriesASessionID(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	raw := MakeLoginSuccess(uuid, "Notch", 776)

	off := afterLoginSuccessProperties(t, raw)
	if len(raw)-off != 16 {
		t.Fatalf("packet has %d bytes after properties, want 16", len(raw)-off)
	}
	if bytes.Equal(raw[off:], make([]byte, 16)) {
		t.Error("session id is all zeroes")
	}
	// Version 4 and the RFC 4122 variant, the shape a client parses.
	if raw[off+6]&0xF0 != 0x40 || raw[off+8]&0xC0 != 0x80 {
		t.Errorf("session id is not a version 4 UUID: %x", raw[off:])
	}
}

// Two different logins must not share a session id.
func TestLoginSuccessSessionIDsDiffer(t *testing.T) {
	uuid := bytes.Repeat([]byte{0x07}, 16)
	first := MakeLoginSuccess(uuid, "Notch", 776)
	second := MakeLoginSuccess(uuid, "Notch", 776)
	if bytes.Equal(first, second) {
		t.Error("session id repeated across logins")
	}
}

// Frame length, packet id, uuid, username and the property count, in that order.
func afterLoginSuccessProperties(t *testing.T, raw []byte) int {
	t.Helper()
	_, off, _ := ReadVarInt(raw, 0)
	_, off, _ = ReadVarInt(raw, off)
	off += 16
	nameLen, off, _ := ReadVarInt(raw, off)
	off += int(nameLen)
	_, off, _ = ReadVarInt(raw, off)
	return off
}

// A status handshake carries protocol version -1, the case that used to hang.
func TestStatusHandshakeIsWellFormed(t *testing.T) {
	raw := MakeStatusHandshake("192.168.1.100", 25565)
	hs, err := ParseHandshake(raw)
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
	raw, err := MakeStatusResponse("{\"text\":\"sleeping\"}", 10, 0, "", "", 770)
	if err != nil {
		t.Fatal(err)
	}
	_, off, _ := ReadVarInt(raw, 0)
	_, off, _ = ReadVarInt(raw, off)
	strLen, off, _ := ReadVarInt(raw, off)
	var decoded StatusPayload
	if err := json.Unmarshal(raw[off:off+int(strLen)], &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version.Protocol != 770 {
		t.Errorf("protocol = %d, want 770", decoded.Version.Protocol)
	}
}

// The bug this replaces: the old single Read parser learned nothing from any
// server that had an icon, because the response spans segments.
func TestReadFramedPacketReassemblesASplitResponse(t *testing.T) {
	icon := "data:image/png;base64," + strings.Repeat("A", 12000)
	frame, err := MakeStatusResponse(config.DefaultMOTDSleeping, 20, 3, icon, "1.21.4", 769)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) < 12000 {
		t.Fatalf("frame is only %d bytes, the test needs one that spans segments", len(frame))
	}

	body, err := ReadFramedPacket(iotest.OneByteReader(bytes.NewReader(frame)), MaxStatusResponseBytes)
	if err != nil {
		t.Fatalf("readFramedPacket: %v", err)
	}

	payload, err := ParseStatusPayload(body)
	if err != nil {
		t.Fatalf("parseStatusPayload: %v", err)
	}
	if payload.Version.Name != "1.21.4" || payload.Version.Protocol != 769 {
		t.Errorf("version = %+v", payload.Version)
	}
	if payload.Players.Max != 20 || payload.Players.Online != 3 {
		t.Errorf("players = %+v", payload.Players)
	}
}

func TestReadFramedPacketRejectsAnOversizedLength(t *testing.T) {
	frame := append(WriteVarInt(int32(MaxStatusResponseBytes+1)), 0x00)

	if _, err := ReadFramedPacket(bytes.NewReader(frame), MaxStatusResponseBytes); !errors.Is(err, ErrPacketTooBig) {
		t.Errorf("err = %v, want ErrPacketTooBig", err)
	}
}

func TestReadFramedPacketRejectsAnEmptyPacket(t *testing.T) {
	if _, err := ReadFramedPacket(bytes.NewReader([]byte{0x00}), MaxStatusResponseBytes); !errors.Is(err, ErrShortPacket) {
		t.Errorf("err = %v, want ErrShortPacket", err)
	}
}

func TestReadFramedPacketFailsOnATruncatedBody(t *testing.T) {
	frame := append(WriteVarInt(10), 1, 2, 3)

	if _, err := ReadFramedPacket(bytes.NewReader(frame), MaxStatusResponseBytes); err == nil {
		t.Error("a body shorter than its length prefix should fail")
	}
}

func TestParseStatusPayloadRejectsAnotherPacketID(t *testing.T) {
	body := append(WriteVarInt(packetIDPing), WriteString("{}")...)

	if _, err := ParseStatusPayload(body); !errors.Is(err, ErrWrongPacketID) {
		t.Errorf("err = %v, want ErrWrongPacketID", err)
	}
}
