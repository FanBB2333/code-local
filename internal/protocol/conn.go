package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	ackInterval    = 2 * time.Second
	keepAliveInterval = 5 * time.Second
)

// Conn manages a WebSocket connection speaking VS Code's binary frame protocol.
type Conn struct {
	ws *websocket.Conn

	outgoingID  atomic.Uint32
	incomingAck atomic.Uint32

	mu       sync.Mutex
	closed   bool
	closeCh  chan struct{}
	debug    bool

	// Channels for received messages
	regularCh chan *Frame
	controlCh chan *Frame
}

// Dial connects to code-server's WebSocket endpoint with authentication.
func Dial(ctx context.Context, wsURL, cookie, origin string) (*Conn, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 30 * time.Second,
	}

	header := http.Header{}
	header.Set("Cookie", cookie)
	header.Set("Origin", origin)

	ws, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("websocket dial: %w", err)
	}

	c := &Conn{
		ws:        ws,
		closeCh:   make(chan struct{}),
		regularCh: make(chan *Frame, 64),
		controlCh: make(chan *Frame, 16),
	}

	go c.readLoop()
	go c.keepAliveLoop()

	return c, nil
}

// SetDebug enables debug logging to stderr.
func (c *Conn) SetDebug(on bool) {
	c.debug = on
}

func (c *Conn) readLoop() {
	defer func() {
		close(c.regularCh)
		close(c.closeCh)
	}()
	for {
		msgType, data, err := c.ws.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.closed = true
			c.mu.Unlock()
			return
		}

		if c.debug {
			fmt.Fprintf(os.Stderr, "[ws] raw: wsType=%d len=%d\n", msgType, len(data))
			if len(data) > 0 && len(data) <= 32 {
				fmt.Fprintf(os.Stderr, "[ws]   raw hex: %x\n", data)
			} else if len(data) > 32 {
				fmt.Fprintf(os.Stderr, "[ws]   raw hex (first 32): %x...\n", data[:32])
			}
		}

		// A single WebSocket message may contain multiple frames
		for offset := 0; offset < len(data); {
			frame, err := DecodeFrame(data[offset:])
			if err != nil {
				if c.debug {
					fmt.Fprintf(os.Stderr, "[ws] frame decode error at offset %d: %v\n", offset, err)
				}
				break
			}
			frameLen := HeaderSize + len(frame.Payload)
			offset += frameLen

			if c.debug {
				fmt.Fprintf(os.Stderr, "[ws] frame: type=%d id=%d ack=%d payload=%d\n",
					frame.Type, frame.ID, frame.Ack, len(frame.Payload))
			}

			switch frame.Type {
			case MessageRegular:
				c.incomingAck.Store(frame.ID)
				select {
				case c.regularCh <- frame:
				default:
				}
			case MessageControl:
				select {
				case c.controlCh <- frame:
				default:
				}
			case MessageAck, MessageKeepAlive:
				// Ack/keepalive — nothing to do for now
			}
		}
	}
}

func (c *Conn) keepAliveLoop() {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.sendFrame(&Frame{
				Type: MessageKeepAlive,
				Ack:  c.incomingAck.Load(),
			})
		case <-c.closeCh:
			return
		}
	}
}

func (c *Conn) sendFrame(f *Frame) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("connection closed")
	}
	return c.ws.WriteMessage(websocket.BinaryMessage, EncodeFrame(f))
}

// SendControl sends a Control message (used during handshake).
func (c *Conn) SendControl(payload []byte) error {
	return c.sendFrame(&Frame{
		Type:    MessageControl,
		Payload: payload,
	})
}

// SendRegular sends a Regular message (used for IPC), returns the message ID.
func (c *Conn) SendRegular(payload []byte) (uint32, error) {
	id := c.outgoingID.Add(1)
	return id, c.sendFrame(&Frame{
		Type:    MessageRegular,
		ID:      id,
		Ack:     c.incomingAck.Load(),
		Payload: payload,
	})
}

// RecvControl waits for a Control message with timeout.
func (c *Conn) RecvControl(timeout time.Duration) (*Frame, error) {
	select {
	case f := <-c.controlCh:
		return f, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("control message timeout")
	case <-c.closeCh:
		return nil, fmt.Errorf("connection closed")
	}
}

// RegularMessages returns the channel for received Regular messages.
func (c *Conn) RegularMessages() <-chan *Frame {
	return c.regularCh
}

// Close closes the underlying WebSocket connection.
func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return c.ws.Close()
}

// Handshake performs the VS Code Server handshake over the connection.
// code-server uses without-connection-token, so auth token is "00000000000000000000".
func (c *Conn) Handshake() error {
	// Phase 1: send auth request
	authMsg, _ := json.Marshal(map[string]string{
		"type": "auth",
		"auth": "00000000000000000000",
	})
	if err := c.SendControl(authMsg); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}

	// Receive sign response
	signFrame, err := c.RecvControl(10 * time.Second)
	if err != nil {
		return fmt.Errorf("recv sign: %w", err)
	}

	var signMsg map[string]interface{}
	if err := json.Unmarshal(signFrame.Payload, &signMsg); err != nil {
		return fmt.Errorf("parse sign: %w", err)
	}

	if signMsg["type"] == "error" {
		return fmt.Errorf("auth rejected: %v", signMsg["reason"])
	}

	// Phase 2: send connection type request (Management = 1)
	connMsg, _ := json.Marshal(map[string]interface{}{
		"type":                  "connectionType",
		"signedData":            signMsg["signedData"],
		"desiredConnectionType": 1,
	})
	if err := c.SendControl(connMsg); err != nil {
		return fmt.Errorf("send connectionType: %w", err)
	}

	// Receive OK
	okFrame, err := c.RecvControl(10 * time.Second)
	if err != nil {
		return fmt.Errorf("recv ok: %w", err)
	}

	var okMsg map[string]interface{}
	if err := json.Unmarshal(okFrame.Payload, &okMsg); err != nil {
		return fmt.Errorf("parse ok: %w", err)
	}

	if okMsg["type"] == "error" {
		return fmt.Errorf("connection rejected: %v", okMsg["reason"])
	}

	return nil
}
