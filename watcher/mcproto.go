package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

var (
	ErrIncompleteVarInt = errors.New("incomplete VarInt")
	ErrVarIntTooBig     = errors.New("VarInt too big")
	ErrShortPacket      = errors.New("packet ended early")
	ErrWrongPacketID    = errors.New("unexpected packet id")
)

const (
	maxUsernameLen = 16
	packetIDStatus = 0x00
	packetIDPing   = 0x01
	// Transfer lives in the configuration state, not the login state.
	packetIDTransfer     = 0x0B
	packetIDLoginSuccess = 0x02
)

func readVarInt(data []byte, offset int) (int32, int, error) {
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

// Negative values encode as five bytes in two's complement, the same as the
// reference implementation. Shifting a negative int instead would never
// terminate, which is what the Python version did.
func writeVarInt(value int32) []byte {
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

func writeString(s string) []byte {
	b := []byte(s)
	return append(writeVarInt(int32(len(b))), b...)
}

// Every packet on the wire is its own length followed by the body.
func framePacket(body []byte) []byte {
	return append(writeVarInt(int32(len(body))), body...)
}

type Handshake struct {
	ProtocolVersion int32
	ServerAddress   string
	ServerPort      uint16
	NextState       int32
	// Offset just past the handshake packet, the client may have sent more.
	End int
}

func parseHandshake(data []byte) (*Handshake, error) {
	pktLen, off, err := readVarInt(data, 0)
	if err != nil {
		return nil, err
	}
	if pktLen < 0 {
		return nil, ErrShortPacket
	}
	end := off + int(pktLen)

	pktID, off, err := readVarInt(data, off)
	if err != nil {
		return nil, err
	}
	if pktID != packetIDStatus {
		return nil, ErrWrongPacketID
	}
	protoVer, off, err := readVarInt(data, off)
	if err != nil {
		return nil, err
	}
	addrLen, off, err := readVarInt(data, off)
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

	nextState, _, err := readVarInt(data, off)
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

type statusPayload struct {
	Version     statusVersion   `json:"version"`
	Players     statusPlayers   `json:"players"`
	Description json.RawMessage `json:"description"`
	Favicon     string          `json:"favicon,omitempty"`
}

func makeStatusResponse(motdJSON string, maxPlayers, online int, icon string, versionName string, versionProtocol int) ([]byte, error) {
	payload := statusPayload{
		Version:     statusVersion{Name: versionName, Protocol: versionProtocol},
		Players:     statusPlayers{Max: maxPlayers, Online: online},
		Description: json.RawMessage(motdJSON),
		Favicon:     icon,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The MOTD may contain characters Go would escape as < by default.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	encoded := bytes.TrimRight(buf.Bytes(), "\n")

	body := append(writeVarInt(packetIDStatus), writeString(string(encoded))...)
	return framePacket(body), nil
}

func makePingResponse(payload int64) []byte {
	body := writeVarInt(packetIDPing)
	body = binary.BigEndian.AppendUint64(body, uint64(payload))
	return framePacket(body)
}

// Returns the username and its 16 byte UUID, both empty when the packet does
// not parse or the name breaks the 16 character limit.
func parseLoginStart(data []byte) (string, []byte, error) {
	_, off, err := readVarInt(data, 0)
	if err != nil {
		return "", nil, err
	}
	pktID, off, err := readVarInt(data, off)
	if err != nil {
		return "", nil, err
	}
	if pktID != packetIDStatus {
		return "", nil, ErrWrongPacketID
	}
	nameLen, off, err := readVarInt(data, off)
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

// Strict error handling byte exists only in protocols 766-767 (1.20.5-1.21.1); other versions crash with "1 extra byte".
func makeLoginSuccess(uuid []byte, username string, protocolVersion int32) []byte {
	body := writeVarInt(packetIDLoginSuccess)
	body = append(body, uuid...)
	body = append(body, writeString(username)...)
	body = append(body, writeVarInt(0)...) // no properties
	if protocolVersion >= 766 && protocolVersion <= 767 {
		body = append(body, 0x01) // strict error handling
	}
	return framePacket(body)
}

// Login state disconnect, so the client shows the message instead of an error.
func makeLoginDisconnect(reasonJSON string) []byte {
	body := append(writeVarInt(packetIDStatus), writeString(reasonJSON)...)
	return framePacket(body)
}

func makeTransferPacket(host string, port int) []byte {
	body := append(writeVarInt(packetIDTransfer), writeString(host)...)
	body = append(body, writeVarInt(int32(port))...)
	return framePacket(body)
}

// Handshake a real server the way a client would, to tell an open port apart
// from a server that is still booting.
func makeStatusHandshake(host string, port int) []byte {
	body := writeVarInt(packetIDStatus)
	body = append(body, writeVarInt(-1)...) // protocol version, any value works
	body = append(body, writeString(host)...)
	body = binary.BigEndian.AppendUint16(body, uint16(port))
	body = append(body, writeVarInt(1)...) // next state: status
	return framePacket(body)
}

func makeStatusRequest() []byte {
	return framePacket(writeVarInt(packetIDStatus))
}
