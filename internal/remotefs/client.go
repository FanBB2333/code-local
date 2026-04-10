package remotefs

import (
	"fmt"
	"time"

	"github.com/FanBB2333/code-local/internal/protocol"
)

const defaultTimeout = 30 * time.Second

// Client provides remote file system operations over VS Code's IPC protocol.
type Client struct {
	ipc *protocol.IPCClient
}

func NewClient(ipc *protocol.IPCClient) *Client {
	return &Client{ipc: ipc}
}

// Stat returns file metadata.
func (c *Client) Stat(path string) (*FileStat, error) {
	result, err := c.ipc.Call(ChannelName, "stat", []interface{}{FileURI(path)}, defaultTimeout)
	if err != nil {
		return nil, c.wrapError("stat", path, err)
	}
	return parseStat(result)
}

// ReadDir lists directory contents.
func (c *Client) ReadDir(path string) ([]DirEntry, error) {
	result, err := c.ipc.Call(ChannelName, "readdir", []interface{}{FileURI(path)}, defaultTimeout)
	if err != nil {
		return nil, c.wrapError("readdir", path, err)
	}
	return parseDirEntries(result)
}

// ReadFile reads an entire file.
func (c *Client) ReadFile(path string) ([]byte, error) {
	result, err := c.ipc.Call(ChannelName, "readFile", []interface{}{FileURI(path)}, defaultTimeout)
	if err != nil {
		return nil, c.wrapError("readFile", path, err)
	}
	if data, ok := result.([]byte); ok {
		return data, nil
	}
	return nil, fmt.Errorf("readFile %s: unexpected result type %T", path, result)
}

// WriteFile writes an entire file.
func (c *Client) WriteFile(path string, data []byte, create, overwrite bool) error {
	opts := map[string]interface{}{
		"create":    create,
		"overwrite": overwrite,
		"unlock":    false,
	}
	_, err := c.ipc.Call(ChannelName, "writeFile", []interface{}{FileURI(path), data, opts}, defaultTimeout)
	if err != nil {
		return c.wrapError("writeFile", path, err)
	}
	return nil
}

// Mkdir creates a directory.
func (c *Client) Mkdir(path string) error {
	_, err := c.ipc.Call(ChannelName, "mkdir", []interface{}{FileURI(path)}, defaultTimeout)
	if err != nil {
		return c.wrapError("mkdir", path, err)
	}
	return nil
}

// Delete removes a file or directory.
func (c *Client) Delete(path string, recursive bool) error {
	opts := map[string]interface{}{
		"recursive": recursive,
		"useTrash":  false,
	}
	_, err := c.ipc.Call(ChannelName, "delete", []interface{}{FileURI(path), opts}, defaultTimeout)
	if err != nil {
		return c.wrapError("delete", path, err)
	}
	return nil
}

// Rename moves/renames a file or directory.
func (c *Client) Rename(oldPath, newPath string, overwrite bool) error {
	opts := map[string]interface{}{
		"overwrite": overwrite,
	}
	_, err := c.ipc.Call(ChannelName, "rename", []interface{}{FileURI(oldPath), FileURI(newPath), opts}, defaultTimeout)
	if err != nil {
		return c.wrapError("rename", oldPath, err)
	}
	return nil
}

// Watch subscribes to file change events for a path.
func (c *Client) Watch(sessionID, reqID, path string, recursive bool, handler func([]FileChange)) (func(), error) {
	// Start watch
	watchOpts := map[string]interface{}{
		"recursive": recursive,
		"excludes":  []interface{}{},
	}
	_, err := c.ipc.Call(ChannelName, "watch", []interface{}{sessionID, reqID, FileURI(path), watchOpts}, defaultTimeout)
	if err != nil {
		return nil, c.wrapError("watch", path, err)
	}

	// Subscribe to fileChange events
	unsub, err := c.ipc.Listen(ChannelName, "fileChange", []interface{}{sessionID}, func(data interface{}) {
		changes := parseFileChanges(data)
		if len(changes) > 0 {
			handler(changes)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("listen fileChange: %w", err)
	}

	return func() {
		unsub()
		c.ipc.Call(ChannelName, "unwatch", []interface{}{sessionID, reqID}, defaultTimeout)
	}, nil
}

func (c *Client) wrapError(op, path string, err error) error {
	if ipcErr, ok := err.(*protocol.IPCError); ok {
		return &RemoteError{Code: ipcErr.Name, Message: ipcErr.Message}
	}
	return fmt.Errorf("%s %s: %w", op, path, err)
}

func parseStat(v interface{}) (*FileStat, error) {
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("stat: unexpected type %T", v)
	}
	return &FileStat{
		Type:        FileType(intVal(m["type"])),
		Mtime:       int64(intVal(m["mtime"])),
		Ctime:       int64(intVal(m["ctime"])),
		Size:        int64(intVal(m["size"])),
		Permissions: FilePermission(intVal(m["permissions"])),
	}, nil
}

func parseDirEntries(v interface{}) ([]DirEntry, error) {
	arr, ok := v.([]interface{})
	if !ok {
		return nil, fmt.Errorf("readdir: unexpected type %T", v)
	}
	entries := make([]DirEntry, 0, len(arr))
	for _, item := range arr {
		pair, ok := item.([]interface{})
		if !ok || len(pair) < 2 {
			continue
		}
		name, _ := pair[0].(string)
		ft := FileType(intVal(pair[1]))
		entries = append(entries, DirEntry{Name: name, Type: ft})
	}
	return entries, nil
}

func parseFileChanges(v interface{}) []FileChange {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	changes := make([]FileChange, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		change := FileChange{
			Type: FileChangeType(intVal(m["type"])),
		}
		if res, ok := m["resource"].(map[string]interface{}); ok {
			change.Resource, _ = res["path"].(string)
		}
		changes = append(changes, change)
	}
	return changes
}

func intVal(v interface{}) int {
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
