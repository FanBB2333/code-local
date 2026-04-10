package protocol

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// VS Code IPC protocol over the binary frame connection.
// Request: header=[type, id, channelName, commandName], body=arg
// Response: header=[type] or header=[type, id], body=data

const (
	RequestPromise      = 100
	RequestPromiseCancel = 101
	RequestEventListen  = 102
	RequestEventDispose = 103

	ResponseInitialize    = 200
	ResponsePromiseSuccess = 201
	ResponsePromiseError   = 202
	ResponsePromiseErrorObj = 203
	ResponseEventFire      = 204
)

type IPCResponse struct {
	Type int
	ID   int
	Data interface{}
}

type IPCError struct {
	Message string
	Name    string
}

func (e *IPCError) Error() string {
	return fmt.Sprintf("%s: %s", e.Name, e.Message)
}

// IPCClient sends IPC requests and receives responses over a Conn.
type IPCClient struct {
	conn        *Conn
	nextID      atomic.Int64
	initialized chan struct{}
	initOnce    sync.Once

	mu       sync.Mutex
	handlers map[int]chan *IPCResponse
	eventHandlers map[int]func(interface{})
}

func NewIPCClient(conn *Conn) *IPCClient {
	c := &IPCClient{
		conn:          conn,
		initialized:   make(chan struct{}),
		handlers:      make(map[int]chan *IPCResponse),
		eventHandlers: make(map[int]func(interface{})),
	}
	go c.dispatchLoop()
	return c
}

func (c *IPCClient) dispatchLoop() {
	for frame := range c.conn.RegularMessages() {
		r := NewReader(frame.Payload)

		header, err := Deserialize(r)
		if err != nil {
			continue
		}
		body, _ := Deserialize(r)

		headerArr, ok := header.([]interface{})
		if !ok || len(headerArr) == 0 {
			continue
		}

		respType, ok := toInt(headerArr[0])
		if !ok {
			continue
		}

		switch respType {
		case ResponseInitialize:
			c.initOnce.Do(func() { close(c.initialized) })

		case ResponsePromiseSuccess, ResponsePromiseError, ResponsePromiseErrorObj:
			if len(headerArr) < 2 {
				continue
			}
			id, ok := toInt(headerArr[1])
			if !ok {
				continue
			}
			resp := &IPCResponse{Type: respType, ID: id, Data: body}
			c.mu.Lock()
			ch, exists := c.handlers[id]
			if exists {
				delete(c.handlers, id)
			}
			c.mu.Unlock()
			if exists {
				ch <- resp
			}

		case ResponseEventFire:
			if len(headerArr) < 2 {
				continue
			}
			id, ok := toInt(headerArr[1])
			if !ok {
				continue
			}
			c.mu.Lock()
			handler := c.eventHandlers[id]
			c.mu.Unlock()
			if handler != nil {
				go handler(body)
			}
		}
	}
}

// WaitInitialized waits for the server's Initialize message.
func (c *IPCClient) WaitInitialized(timeout time.Duration) error {
	select {
	case <-c.initialized:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("IPC initialization timeout")
	}
}

// Call sends a Promise request and waits for the response.
func (c *IPCClient) Call(channelName, command string, arg interface{}, timeout time.Duration) (interface{}, error) {
	id := int(c.nextID.Add(1))

	// Register response handler
	ch := make(chan *IPCResponse, 1)
	c.mu.Lock()
	c.handlers[id] = ch
	c.mu.Unlock()

	// Build message: serialize([RequestPromise, id, channelName, command]) + serialize(arg)
	w := &Writer{}
	Serialize(w, []interface{}{RequestPromise, id, channelName, command})
	Serialize(w, arg)

	if _, err := c.conn.SendRegular(w.Bytes()); err != nil {
		c.mu.Lock()
		delete(c.handlers, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("send request: %w", err)
	}

	// Wait for response
	select {
	case resp := <-ch:
		switch resp.Type {
		case ResponsePromiseSuccess:
			return resp.Data, nil
		case ResponsePromiseError:
			if m, ok := resp.Data.(map[string]interface{}); ok {
				msg, _ := m["message"].(string)
				name, _ := m["name"].(string)
				return nil, &IPCError{Message: msg, Name: name}
			}
			return nil, fmt.Errorf("IPC error: %v", resp.Data)
		case ResponsePromiseErrorObj:
			return nil, fmt.Errorf("IPC error object: %v", resp.Data)
		default:
			return nil, fmt.Errorf("unexpected response type: %d", resp.Type)
		}
	case <-time.After(timeout):
		c.mu.Lock()
		delete(c.handlers, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("IPC call timeout: %s.%s", channelName, command)
	}
}

// Listen subscribes to an event on a channel and returns a function to unsubscribe.
func (c *IPCClient) Listen(channelName, event string, arg interface{}, handler func(interface{})) (unsubscribe func(), err error) {
	id := int(c.nextID.Add(1))

	c.mu.Lock()
	c.eventHandlers[id] = handler
	c.mu.Unlock()

	w := &Writer{}
	Serialize(w, []interface{}{RequestEventListen, id, channelName, event})
	Serialize(w, arg)

	if _, err := c.conn.SendRegular(w.Bytes()); err != nil {
		c.mu.Lock()
		delete(c.eventHandlers, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("send event listen: %w", err)
	}

	return func() {
		c.mu.Lock()
		delete(c.eventHandlers, id)
		c.mu.Unlock()

		w := &Writer{}
		Serialize(w, []interface{}{RequestEventDispose, id})
		Serialize(w, nil)
		c.conn.SendRegular(w.Bytes())
	}, nil
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint32:
		return int(n), true
	default:
		return 0, false
	}
}
