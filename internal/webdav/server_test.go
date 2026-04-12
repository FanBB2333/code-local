package webdav

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/FanBB2333/code-local/internal/remotefs"
)

type memoryRemoteFS struct {
	mu      sync.Mutex
	files   map[string][]byte
	entries map[string]map[string]remotefs.FileType
}

func newMemoryRemoteFS() *memoryRemoteFS {
	return &memoryRemoteFS{
		files: map[string][]byte{
			"/workspace/demo/hello.txt": []byte("hello from remote"),
		},
		entries: map[string]map[string]remotefs.FileType{
			"/workspace/demo": {
				"hello.txt": remotefs.FileTypeFile,
			},
		},
	}
}

func (m *memoryRemoteFS) Stat(path string) (*remotefs.FileStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.entries[path]; ok {
		return &remotefs.FileStat{Type: remotefs.FileTypeDirectory}, nil
	}
	data, ok := m.files[path]
	if !ok {
		return nil, &remotefs.RemoteError{Code: "EntryNotFound", Message: path}
	}
	return &remotefs.FileStat{
		Type: remotefs.FileTypeFile,
		Size: int64(len(data)),
	}, nil
}

func (m *memoryRemoteFS) ReadDir(path string) ([]remotefs.DirEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dir, ok := m.entries[path]
	if !ok {
		return nil, &remotefs.RemoteError{Code: "EntryNotFound", Message: path}
	}
	var out []remotefs.DirEntry
	for name, typ := range dir {
		out = append(out, remotefs.DirEntry{Name: name, Type: typ})
	}
	return out, nil
}

func (m *memoryRemoteFS) ReadFile(path string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[path]
	if !ok {
		return nil, &remotefs.RemoteError{Code: "EntryNotFound", Message: path}
	}
	return append([]byte(nil), data...), nil
}

func (m *memoryRemoteFS) WriteFile(path string, data []byte, create, overwrite bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[path] = append([]byte(nil), data...)
	parent := "/workspace/demo"
	if _, ok := m.entries[parent]; !ok {
		m.entries[parent] = map[string]remotefs.FileType{}
	}
	m.entries[parent][strings.TrimPrefix(path, parent+"/")] = remotefs.FileTypeFile
	return nil
}

func (m *memoryRemoteFS) Mkdir(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[path] = map[string]remotefs.FileType{}
	return nil
}

func (m *memoryRemoteFS) Delete(path string, recursive bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[path]; ok && recursive {
		return &remotefs.RemoteError{Code: "InvalidDelete", Message: "file delete must not be recursive"}
	}
	delete(m.files, path)
	delete(m.entries["/workspace/demo"], strings.TrimPrefix(path, "/workspace/demo/"))
	return nil
}

func (m *memoryRemoteFS) Rename(oldPath, newPath string, overwrite bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.files[oldPath]
	delete(m.files, oldPath)
	m.files[newPath] = data
	return nil
}

type multistatus struct {
	Responses []struct {
		Href string `xml:"href"`
	} `xml:"response"`
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestServerGETPUTDeleteAndPropfind(t *testing.T) {
	remote := newMemoryRemoteFS()
	server, err := NewServer(remote, "/workspace/demo", freePort(t))
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close()

	go func() {
		_ = server.Serve()
	}()

	baseURL := "http://" + server.Addr()

	req, err := http.NewRequestWithContext(context.Background(), "PROPFIND", baseURL+"/", nil)
	if err != nil {
		t.Fatalf("new propfind request: %v", err)
	}
	req.Header.Set("Depth", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("propfind: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("expected 207 for PROPFIND, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read propfind body: %v", err)
	}
	var ms multistatus
	if err := xml.Unmarshal(body, &ms); err != nil {
		t.Fatalf("unmarshal propfind response: %v", err)
	}
	if len(ms.Responses) < 2 {
		t.Fatalf("expected directory listing in propfind response, got %d entries", len(ms.Responses))
	}

	getResp, err := http.Get(baseURL + "/hello.txt")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer getResp.Body.Close()
	getBody, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("read get body: %v", err)
	}
	if string(getBody) != "hello from remote" {
		t.Fatalf("unexpected get body %q", string(getBody))
	}

	putReq, err := http.NewRequestWithContext(context.Background(), http.MethodPut, baseURL+"/new.txt", bytes.NewReader([]byte("new file")))
	if err != nil {
		t.Fatalf("new put request: %v", err)
	}
	putResp, err := http.DefaultClient.Do(putReq)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated && putResp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected put status %d", putResp.StatusCode)
	}

	if got := string(remote.files["/workspace/demo/new.txt"]); got != "new file" {
		t.Fatalf("expected written file, got %q", got)
	}

	delReq, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, baseURL+"/new.txt", nil)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	delResp, err := http.DefaultClient.Do(delReq)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected delete status %d", delResp.StatusCode)
	}
	if _, ok := remote.files["/workspace/demo/new.txt"]; ok {
		t.Fatal("expected file to be deleted")
	}
}
