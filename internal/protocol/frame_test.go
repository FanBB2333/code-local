package protocol

import (
	"reflect"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	cases := []*Frame{
		{Type: MessageRegular, ID: 1, Ack: 0, Payload: []byte("hello")},
		{Type: MessageControl, ID: 0, Ack: 0, Payload: []byte(`{"type":"auth"}`)},
		{Type: MessageKeepAlive, ID: 0, Ack: 42, Payload: nil},
		{Type: MessageAck, ID: 0, Ack: 100, Payload: nil},
		{Type: MessageRegular, ID: 0xFFFFFFFF, Ack: 0xFFFFFFFF, Payload: make([]byte, 1024)},
	}

	for _, f := range cases {
		data := EncodeFrame(f)
		got, err := DecodeFrame(data)
		if err != nil {
			t.Fatalf("DecodeFrame(%v): %v", f.Type, err)
		}
		if got.Type != f.Type {
			t.Fatalf("type: %d != %d", got.Type, f.Type)
		}
		if got.ID != f.ID {
			t.Fatalf("id: %d != %d", got.ID, f.ID)
		}
		if got.Ack != f.Ack {
			t.Fatalf("ack: %d != %d", got.Ack, f.Ack)
		}
		if f.Payload == nil {
			if len(got.Payload) != 0 {
				t.Fatalf("payload: expected empty, got %d bytes", len(got.Payload))
			}
		} else if !reflect.DeepEqual(got.Payload, f.Payload) {
			t.Fatalf("payload mismatch")
		}
	}
}

func TestDecodeFrameTooShort(t *testing.T) {
	_, err := DecodeFrame([]byte{0, 0, 0})
	if err == nil {
		t.Fatal("expected error for short frame")
	}
}

func TestDecodeFrameTruncated(t *testing.T) {
	f := &Frame{Type: MessageRegular, Payload: []byte("hello")}
	data := EncodeFrame(f)
	// Truncate the payload
	_, err := DecodeFrame(data[:HeaderSize+2])
	if err == nil {
		t.Fatal("expected error for truncated frame")
	}
}

func TestFrameHeaderSize(t *testing.T) {
	f := &Frame{Type: MessageControl, Payload: nil}
	data := EncodeFrame(f)
	if len(data) != HeaderSize {
		t.Fatalf("empty frame should be %d bytes, got %d", HeaderSize, len(data))
	}
}
