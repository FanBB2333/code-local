package protocol

import (
	"encoding/binary"
	"fmt"
)

// VS Code binary frame protocol.
// Each message has a 13-byte header: Type(1) + ID(4) + ACK(4) + DataLen(4).
// All multi-byte integers are big-endian.

const HeaderSize = 13

type MessageType uint8

const (
	MessageNone         MessageType = 0
	MessageRegular      MessageType = 1
	MessageControl      MessageType = 2
	MessageAck          MessageType = 3
	MessageDisconnect   MessageType = 5
	MessageReplayReq    MessageType = 6
	MessagePause        MessageType = 7
	MessageResume       MessageType = 8
	MessageKeepAlive    MessageType = 9
)

type Frame struct {
	Type    MessageType
	ID      uint32
	Ack     uint32
	Payload []byte
}

func EncodeFrame(f *Frame) []byte {
	buf := make([]byte, HeaderSize+len(f.Payload))
	buf[0] = byte(f.Type)
	binary.BigEndian.PutUint32(buf[1:5], f.ID)
	binary.BigEndian.PutUint32(buf[5:9], f.Ack)
	binary.BigEndian.PutUint32(buf[9:13], uint32(len(f.Payload)))
	copy(buf[HeaderSize:], f.Payload)
	return buf
}

func DecodeFrame(data []byte) (*Frame, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("frame too short: %d < %d", len(data), HeaderSize)
	}
	dataLen := binary.BigEndian.Uint32(data[9:13])
	if len(data) < int(HeaderSize+dataLen) {
		return nil, fmt.Errorf("frame truncated: have %d, need %d", len(data), HeaderSize+dataLen)
	}
	return &Frame{
		Type:    MessageType(data[0]),
		ID:      binary.BigEndian.Uint32(data[1:5]),
		Ack:     binary.BigEndian.Uint32(data[5:9]),
		Payload: data[HeaderSize : HeaderSize+dataLen],
	}, nil
}
