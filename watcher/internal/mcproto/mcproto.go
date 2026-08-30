package mcproto

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

var (
	ErrIncompleteVarInt = errors.New("incomplete VarInt")
	ErrVarIntTooBig     = errors.New("VarInt too big")
	ErrShortPacket      = errors.New("packet ended early")
	ErrWrongPacketID    = errors.New("unexpected packet id")
	ErrPacketTooBig     = errors.New("packet larger than the allowed limit")
)

const (
	// A status response with an icon is tens of kilobytes, never megabytes.
	MaxStatusResponseBytes = 256 * 1024

	maxUsernameLen = 16
	PacketIDStatus = 0x00
	packetIDPing   = 0x01
	// Transfer lives in the configuration state, not the login state.
	packetIDTransfer     = 0x0B
	packetIDLoginSuccess = 0x02
)

// Login Success grew and lost fields over time: 1.20.5 to 1.21.1 end it with a
// strict error handling byte, 26.2 appends a session id instead.
const (
	strictErrorFirstProtocol = 766
	strictErrorLastProtocol  = 767
	sessionIDFirstProtocol   = 776
)

func ReadVarInt(data []byte, offset int) (int32, int, error) {
	var result uint32
	var shift uint
	for {
		if offset >= len(data) {
			return 0, offset, ErrIncompleteVarInt
		}
		b := data[offset]
		offset++
		result |= uint32(b&0x7F) << shift
		if b&0x80 == 0 {
			break
		}
		shift += 7
		if shift >= 35 {
			return 0, offset, ErrVarIntTooBig
		}
	}
	return int32(result), offset, nil
}

// Negative values need the unsigned shift, shifting a signed int never ends.
func WriteVarInt(value int32) []byte {
	v := uint32(value)
	out := make([]byte, 0, 5)
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			break
		}
	}
	return out
}

func WriteString(s string) []byte {
	b := []byte(s)
	return append(WriteVarInt(int32(len(b))), b...)
}

// Every packet on the wire is its own length followed by the body.
func FramePacket(body []byte) []byte {
	return append(WriteVarInt(int32(len(body))), body...)
}

type Handshake struct {
	ProtocolVersion int32
	ServerAddress   string
	ServerPort      uint16
	NextState       int32
	// Offset just past the handshake packet, the client may have sent more.
	End int
}

func ParseHandshake(data []byte) (*Handshake, error) {
	pktLen, off, err := ReadVarInt(data, 0)
	if err != nil {
		return nil, err
	}
	if pktLen < 0 {
		return nil, ErrShortPacket
	}
	end := off + int(pktLen)

	pktID, off, err := ReadVarInt(data, off)
	if err != nil {
		return nil, err
	}
	if pktID != PacketIDStatus {
		return nil, ErrWrongPacketID
	}
	protoVer, off, err := ReadVarInt(data, off)
	if err != nil {
		return nil, err
	}
	addrLen, off, err := ReadVarInt(data, off)
	if err != nil {
		return nil, err
	}
	if addrLen < 0 || off+int(addrLen) > len(data) {
		return nil, ErrShortPacket
	}
	addr := string(data[off : off+int(addrLen)])
	off += int(addrLen)

	if off+2 > len(data) {
		return nil, ErrShortPacket
	}
	port := binary.BigEndian.Uint16(data[off : off+2])
	off += 2

	nextState, _, err := ReadVarInt(data, off)
	if err != nil {
		return nil, err
	}
	return &Handshake{
		ProtocolVersion: protoVer,
		ServerAddress:   addr,
		ServerPort:      port,
		NextState:       nextState,
		End:             end,
	}, nil
}

type statusVersion struct {
	Name     string `json:"name"`
	Protocol int    `json:"protocol"`
}

type statusPlayers struct {
	Max    int `json:"max"`
	Online int `json:"online"`
}

type StatusPayload struct {
	Version     statusVersion   `json:"version"`
	Players     statusPlayers   `json:"players"`
	Description json.RawMessage `json:"description"`
	Favicon     string          `json:"favicon,omitempty"`
}

func MakeStatusResponse(motdJSON string, maxPlayers, online int, icon string, versionName string, versionProtocol int) ([]byte, error) {
	return encodeStatusPayload(&StatusPayload{
		Version:     statusVersion{Name: versionName, Protocol: versionProtocol},
		Players:     statusPlayers{Max: maxPlayers, Online: online},
		Description: json.RawMessage(motdJSON),
		Favicon:     icon,
	})
}

func encodeStatusPayload(payload *StatusPayload) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The MOTD may contain characters Go would escape as < by default.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	encoded := bytes.TrimRight(buf.Bytes(), "\n")

	body := append(WriteVarInt(PacketIDStatus), WriteString(string(encoded))...)
	return FramePacket(body), nil
}

// A response with an icon is around 10 kB and never arrives in one Read.
// Returns the body without the length prefix.
func ReadFramedPacket(r io.Reader, maxLen int) ([]byte, error) {
	var length int32
	var shift uint
	one := make([]byte, 1)
	for {
		if _, err := io.ReadFull(r, one); err != nil {
			return nil, err
		}
		length |= int32(one[0]&0x7F) << shift
		if one[0]&0x80 == 0 {
			break
		}
		shift += 7
		if shift >= 35 {
			return nil, ErrVarIntTooBig
		}
	}
	if length <= 0 {
		return nil, ErrShortPacket
	}
	if int(length) > maxLen {
		return nil, fmt.Errorf("%w: %d bytes", ErrPacketTooBig, length)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// Body of a status response packet, so packet id followed by the JSON string.
// Fields are kept raw so everything the server sends that we do not model, the
// mod list and the player sample among them, survives being rewritten.
func RewriteStatusResponse(body []byte, motd, icon string) ([]byte, error) {
	pktID, off, err := ReadVarInt(body, 0)
	if err != nil {
		return nil, err
	}
	if pktID != PacketIDStatus {
		return nil, ErrWrongPacketID
	}
	jsonLen, off, err := ReadVarInt(body, off)
	if err != nil {
		return nil, err
	}
	if jsonLen <= 0 || off+int(jsonLen) > len(body) {
		return nil, ErrShortPacket
	}

	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(body[off:off+int(jsonLen)], &fields); err != nil {
		return nil, err
	}
	if motd != "" {
		fields["description"] = json.RawMessage(motd)
	}
	if icon != "" {
		quoted, err := json.Marshal(icon)
		if err != nil {
			return nil, err
		}
		fields["favicon"] = quoted
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return nil, err
	}
	encoded := bytes.TrimRight(buf.Bytes(), "' + NL + '")
	return FramePacket(append(WriteVarInt(PacketIDStatus), WriteString(string(encoded))...)), nil
}

func ParseStatusPayload(body []byte) (*StatusPayload, error) {
	pktID, off, err := ReadVarInt(body, 0)
	if err != nil {
		return nil, err
	}
	if pktID != PacketIDStatus {
		return nil, ErrWrongPacketID
	}
	jsonLen, off, err := ReadVarInt(body, off)
	if err != nil {
		return nil, err
	}
	if jsonLen <= 0 || off+int(jsonLen) > len(body) {
		return nil, ErrShortPacket
	}
	var payload StatusPayload
	if err := json.Unmarshal(body[off:off+int(jsonLen)], &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func MakePingResponse(payload int64) []byte {
	body := WriteVarInt(packetIDPing)
	body = binary.BigEndian.AppendUint64(body, uint64(payload))
	return FramePacket(body)
}

// Returns the username and its 16 byte UUID, both empty when the packet does
// not parse or the name breaks the 16 character limit.
func ParseLoginStart(data []byte) (string, []byte, error) {
	_, off, err := ReadVarInt(data, 0)
	if err != nil {
		return "", nil, err
	}
	pktID, off, err := ReadVarInt(data, off)
	if err != nil {
		return "", nil, err
	}
	if pktID != PacketIDStatus {
		return "", nil, ErrWrongPacketID
	}
	nameLen, off, err := ReadVarInt(data, off)
	if err != nil {
		return "", nil, err
	}
	// UTF-8 worst case for a 16 character name.
	if nameLen <= 0 || nameLen > maxUsernameLen*4 {
		return "", nil, ErrShortPacket
	}
	if off+int(nameLen) > len(data) {
		return "", nil, ErrShortPacket
	}
	name := string(data[off : off+int(nameLen)])
	off += int(nameLen)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxUsernameLen {
		return "", nil, ErrShortPacket
	}
	if off+16 > len(data) {
		return "", nil, ErrShortPacket
	}
	uuid := make([]byte, 16)
	copy(uuid, data[off:off+16])
	return name, uuid, nil
}

// A field too many or too few and the client drops the connection with a
// decoder error, so both trailing fields are tied to the exact protocol range.
func MakeLoginSuccess(uuid []byte, username string, protocolVersion int32) []byte {
	body := WriteVarInt(packetIDLoginSuccess)
	body = append(body, uuid...)
	body = append(body, WriteString(username)...)
	body = append(body, WriteVarInt(0)...) // no properties
	if protocolVersion >= strictErrorFirstProtocol && protocolVersion <= strictErrorLastProtocol {
		body = append(body, 0x01) // strict error handling
	}
	if protocolVersion >= sessionIDFirstProtocol {
		body = append(body, randomUUID()...)
	}
	return FramePacket(body)
}

// The client is transferred away before it ever uses the session, so any well
// formed version 4 UUID does.
func randomUUID() []byte {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return id
	}
	id[6] = id[6]&0x0F | 0x40
	id[8] = id[8]&0x3F | 0x80
	return id
}

// Login state disconnect, so the client shows the message instead of an error.
func MakeLoginDisconnect(reasonJSON string) []byte {
	body := append(WriteVarInt(PacketIDStatus), WriteString(reasonJSON)...)
	return FramePacket(body)
}

func MakeTransferPacket(host string, port int) []byte {
	body := append(WriteVarInt(packetIDTransfer), WriteString(host)...)
	body = append(body, WriteVarInt(int32(port))...)
	return FramePacket(body)
}

// Handshake a real server the way a client would, to tell an open port apart
// from a server that is still booting.
func MakeStatusHandshake(host string, port int) []byte {
	body := WriteVarInt(PacketIDStatus)
	body = append(body, WriteVarInt(-1)...) // protocol version, any value works
	body = append(body, WriteString(host)...)
	body = binary.BigEndian.AppendUint16(body, uint16(port))
	body = append(body, WriteVarInt(1)...) // next state: status
	return FramePacket(body)
}

func MakeStatusRequest() []byte {
	return FramePacket(WriteVarInt(PacketIDStatus))
}

func MakeHandshake(proto int32, addr string, port uint16, next int32) []byte {
	body := WriteVarInt(0x00)
	body = append(body, WriteVarInt(proto)...)
	body = append(body, WriteString(addr)...)
	body = binary.BigEndian.AppendUint16(body, port)
	body = append(body, WriteVarInt(next)...)
	return FramePacket(body)
}

func MakeLoginStart(name string, uuid []byte) []byte {
	body := append(WriteVarInt(0x00), WriteString(name)...)
	body = append(body, uuid...)
	return FramePacket(body)
}
