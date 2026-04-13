package main

import (
	"context"
	"net"
	"strings"
	"testing"

	nfsserver "github.com/FanBB2333/code-local/internal/nfs"
	"github.com/FanBB2333/code-local/internal/remotefs"
)

type testRemoteFS struct{}

func (testRemoteFS) Stat(path string) (*remotefs.FileStat, error) { return nil, nil }
func (testRemoteFS) ReadDir(path string) ([]remotefs.DirEntry, error) {
	return nil, nil
}
func (testRemoteFS) ReadFile(path string) ([]byte, error) { return nil, nil }
func (testRemoteFS) WriteFile(path string, data []byte, create, overwrite bool) error {
	return nil
}
func (testRemoteFS) Mkdir(path string) error                  { return nil }
func (testRemoteFS) Delete(path string, recursive bool) error { return nil }
func (testRemoteFS) Rename(oldPath, newPath string, overwrite bool) error {
	return nil
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

func TestParseBackendDefaultsToNFS(t *testing.T) {
	backend, err := parseBackend("")
	if err != nil {
		t.Fatalf("parseBackend returned error: %v", err)
	}
	if backend != backendNFS {
		t.Fatalf("expected default backend %q, got %q", backendNFS, backend)
	}
}

func TestParseBackendRejectsUnknownBackend(t *testing.T) {
	if _, err := parseBackend("smb"); err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestCreateBackendSupportsNFSAndWebDAV(t *testing.T) {
	for _, backend := range []backendKind{backendNFS, backendWebDAV} {
		server, err := createBackend(context.Background(), backend, testRemoteFS{}, "/workspace/demo", freePort(t), nfsserver.Options{})
		if err != nil {
			t.Fatalf("createBackend(%q) returned error: %v", backend, err)
		}
		if server == nil {
			t.Fatalf("createBackend(%q) returned nil server", backend)
		}
		if err := server.Close(); err != nil {
			t.Fatalf("close backend %q: %v", backend, err)
		}
	}
}

func TestCreateBackendMountCommand(t *testing.T) {
	server, err := createBackend(context.Background(), backendWebDAV, testRemoteFS{}, "/workspace/demo", freePort(t), nfsserver.Options{})
	if err != nil {
		t.Fatalf("createBackend(webdav) returned error: %v", err)
	}
	defer server.Close()

	cmd := server.MountCmd("/tmp/code-local-webdav")
	if cmd == "" {
		t.Fatal("expected non-empty mount command")
	}
}

type testWatchableFS struct {
	testRemoteFS
	watchCalls   int
	unwatchCalls int
}

func (f *testWatchableFS) Watch(sessionID, reqID, path string, recursive bool, handler func([]remotefs.FileChange)) (func(), error) {
	f.watchCalls++
	return func() { f.unwatchCalls++ }, nil
}

func TestWrapRemoteFSStartsAndStopsWatch(t *testing.T) {
	base := &testWatchableFS{}

	wrapped, stop, err := wrapRemoteFS(base, "/workspace/demo")
	if err != nil {
		t.Fatalf("wrapRemoteFS() error = %v", err)
	}
	if _, ok := wrapped.(*remotefs.CachedFS); !ok {
		t.Fatalf("wrapped remote type = %T, want *remotefs.CachedFS", wrapped)
	}
	if base.watchCalls != 1 {
		t.Fatalf("watchCalls = %d, want 1", base.watchCalls)
	}

	stop()

	if base.unwatchCalls != 1 {
		t.Fatalf("unwatchCalls = %d, want 1", base.unwatchCalls)
	}
}

func TestCreateBackendPassesNFSOptions(t *testing.T) {
	server, err := createBackend(
		context.Background(),
		backendNFS,
		testRemoteFS{},
		"/workspace/demo",
		freePort(t),
		nfsserver.Options{Actimeo: 30},
	)
	if err != nil {
		t.Fatalf("createBackend() error = %v", err)
	}
	defer server.Close()

	if !strings.Contains(server.MountCmd("/tmp/code-local"), "actimeo=30") {
		t.Fatalf("mount command %q does not contain actimeo=30", server.MountCmd("/tmp/code-local"))
	}
}
