package terminal

import (
	"fmt"
	"sync"
	"time"

	"github.com/FanBB2333/code-local/internal/protocol"
)

const ChannelName = "remoteterminal"

const (
	// Flow control: ack every 5000 chars of output received
	charCountAckSize = 5000
)

// ProcessInfo describes a running terminal process.
type ProcessInfo struct {
	ID   int
	Pid  int
	Cwd  string
	Name string
}

// Client wraps IPC calls to the remoteterminal channel.
type Client struct {
	ipc *protocol.IPCClient

	mu        sync.Mutex
	charCount int
}

func NewClient(ipc *protocol.IPCClient) *Client {
	return &Client{ipc: ipc}
}

// ListProcesses returns all running terminal processes.
func (c *Client) ListProcesses() ([]ProcessInfo, error) {
	result, err := c.ipc.Call(ChannelName, "$listProcesses", nil, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("listProcesses: %w", err)
	}
	arr, ok := result.([]interface{})
	if !ok {
		return nil, fmt.Errorf("listProcesses: unexpected type %T", result)
	}
	var procs []ProcessInfo
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		p := ProcessInfo{}
		if v, ok := m["pid"]; ok {
			p.Pid = toInt(v)
		}
		if v, ok := m["cwd"]; ok {
			p.Cwd, _ = v.(string)
		}
		if v, ok := m["name"]; ok {
			p.Name, _ = v.(string)
		}
		procs = append(procs, p)
	}
	return procs, nil
}

// CreateProcess creates a new terminal process and returns its persistent ID.
func (c *Client) CreateProcess(cwd string, cols, rows int) (int, error) {
	shellConfig := map[string]interface{}{
		"name":       "Terminal",
		"executable": "/bin/bash",
		"args":       []interface{}{},
	}
	if cwd != "" {
		shellConfig["cwd"] = cwd
	}

	args := map[string]interface{}{
		"shellLaunchConfig": shellConfig,
		"configuration": map[string]interface{}{
			"terminal.integrated.env.windows":  map[string]interface{}{},
			"terminal.integrated.env.osx":      map[string]interface{}{},
			"terminal.integrated.env.linux":    map[string]interface{}{},
			"terminal.integrated.cwd":          "",
			"terminal.integrated.detectLocale": "auto",
		},
		"resolvedVariables":      map[string]interface{}{},
		"envVariableCollections": []interface{}{},
		"workspaceId":            "code-local",
		"workspaceName":          "workspace",
		"workspaceFolders":       []interface{}{},
		"activeWorkspaceFolder":  nil,
		"activeFileResource":     nil,
		"shouldPersistTerminal":  true,
		"options": map[string]interface{}{
			"shellIntegration": map[string]interface{}{
				"enabled":        true,
				"suggestEnabled": false,
				"nonce":          "",
			},
			"windowsUseConptyDll":            false,
			"environmentVariableCollections": nil,
			"workspaceFolder":                nil,
			"isScreenReaderOptimized":        false,
		},
		"cols":           cols,
		"rows":           rows,
		"unicodeVersion": "11",
		"resolverEnv":    nil,
	}

	result, err := c.ipc.Call(ChannelName, "$createProcess", args, 15*time.Second)
	if err != nil {
		return 0, fmt.Errorf("createProcess: %w", err)
	}

	m, ok := result.(map[string]interface{})
	if !ok {
		return 0, fmt.Errorf("createProcess: unexpected result type %T", result)
	}

	id := toInt(m["persistentTerminalId"])
	return id, nil
}

// Start begins the terminal process.
func (c *Client) Start(id int) error {
	_, err := c.ipc.Call(ChannelName, "$start", []interface{}{id}, 10*time.Second)
	return err
}

// Input sends keyboard/text input to the terminal.
func (c *Client) Input(id int, data string) error {
	_, err := c.ipc.Call(ChannelName, "$input", []interface{}{id, data}, 5*time.Second)
	return err
}

// Resize changes the terminal dimensions.
func (c *Client) Resize(id, cols, rows int) error {
	_, err := c.ipc.Call(ChannelName, "$resize", []interface{}{id, cols, rows}, 5*time.Second)
	return err
}

// Shutdown terminates the terminal process.
func (c *Client) Shutdown(id int, immediate bool) error {
	_, err := c.ipc.Call(ChannelName, "$shutdown", []interface{}{id, immediate}, 5*time.Second)
	return err
}

// AcknowledgeDataEvent sends a flow control ack.
func (c *Client) AcknowledgeDataEvent(id, charCount int) error {
	_, err := c.ipc.Call(ChannelName, "$acknowledgeDataEvent", []interface{}{id, charCount}, 5*time.Second)
	return err
}

// SubscribeOutput listens for terminal output events. Returns an unsubscribe function.
func (c *Client) SubscribeOutput(handler func(id int, data string)) (func(), error) {
	return c.ipc.Listen(ChannelName, "$onProcessDataEvent", nil, func(raw interface{}) {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return
		}
		id := toInt(m["id"])

		var data string
		switch ev := m["event"].(type) {
		case string:
			data = ev
		case map[string]interface{}:
			data, _ = ev["data"].(string)
		}

		if data != "" {
			handler(id, data)

			// Flow control: ack periodically
			c.mu.Lock()
			c.charCount += len(data)
			if c.charCount >= charCountAckSize {
				ack := c.charCount
				c.charCount = 0
				c.mu.Unlock()
				c.AcknowledgeDataEvent(id, ack)
			} else {
				c.mu.Unlock()
			}
		}
	})
}

// SubscribeExit listens for terminal exit events. Returns an unsubscribe function.
func (c *Client) SubscribeExit(handler func(id int, code int)) (func(), error) {
	return c.ipc.Listen(ChannelName, "$onProcessExitEvent", nil, func(raw interface{}) {
		m, ok := raw.(map[string]interface{})
		if !ok {
			return
		}
		id := toInt(m["id"])
		code := toInt(m["event"])
		handler(id, code)
	})
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	default:
		return 0
	}
}
