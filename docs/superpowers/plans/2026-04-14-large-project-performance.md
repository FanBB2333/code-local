# Large Project Mount Performance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve responsiveness after mounting large projects by eliminating protocol frame drops, adding shared metadata caching with watch-driven invalidation, and making NFS metadata cache tuning configurable.

**Architecture:** Keep the remote `code-server` protocol unchanged. Fix reliability at the WebSocket frame delivery layer, then wrap `remotefs.Client` with a shared `CachedFS` that both NFS and WebDAV consume. Start one recursive watch at the mounted remote root to invalidate cached directory and stat entries, and expose NFS `actimeo` through CLI/backend options for large read-heavy repositories.

**Tech Stack:** Go, `gorilla/websocket`, existing `internal/cache`, `golang.org/x/sync/singleflight`, `willscott/go-nfs`, standard `testing` package

---

## File Map

- Modify: `internal/protocol/conn.go` - stop dropping regular/control frames when receive queues fill, and make shutdown unblock waiting senders.
- Create: `internal/protocol/conn_test.go` - regression tests for frame delivery under backpressure.
- Modify: `internal/remotefs/fs.go` - add a watch-capable interface and shared cache config defaults.
- Create: `internal/remotefs/cachedfs.go` - shared metadata cache wrapper with invalidation hooks.
- Create: `internal/remotefs/cachedfs_test.go` - cache hit, singleflight, and watch invalidation tests.
- Modify: `cmd/code-local/main.go` - wrap the remote filesystem with `CachedFS`, start/stop the root watch, and parse `--nfs-actimeo`.
- Modify: `cmd/code-local/main_test.go` - cover remote wrapper wiring and NFS option plumbing.
- Modify: `internal/nfs/server.go` - accept NFS options and render configurable `actimeo` in the mount command.
- Create: `internal/nfs/server_test.go` - mount command regression tests for `actimeo`.
- Modify: `README.md` - document large-project guidance and the new tuning flag.
- Modify: `docs/usage.md` - document `--nfs-actimeo` and recommend `nfs` for large repositories.
- Modify: `docs/large-project-performance.md` - link the analysis doc to this implementation plan.
- Modify: `go.mod`, `go.sum` - add `golang.org/x/sync` for `singleflight`.

### Task 1: Make Protocol Frame Delivery Reliable

**Files:**
- Create: `internal/protocol/conn_test.go`
- Modify: `internal/protocol/conn.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/protocol/conn_test.go` with focused backpressure coverage:

```go
package protocol

import (
	"testing"
	"time"
)

func TestDeliverFrameWaitsForRegularBufferDrain(t *testing.T) {
	c := &Conn{
		regularCh: make(chan *Frame, 1),
		controlCh: make(chan *Frame, 1),
		closeCh:   make(chan struct{}),
	}
	c.regularCh <- &Frame{Type: MessageRegular, ID: 1}

	done := make(chan struct{})
	go func() {
		_ = c.deliverFrame(&Frame{Type: MessageRegular, ID: 2})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("deliverFrame returned before the buffer drained")
	case <-time.After(50 * time.Millisecond):
	}

	<-c.regularCh

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deliverFrame stayed blocked after the buffer drained")
	}

	frame := <-c.regularCh
	if frame.ID != 2 {
		t.Fatalf("frame ID = %d, want 2", frame.ID)
	}
}

func TestDeliverFrameReturnsWhenConnectionCloses(t *testing.T) {
	c := &Conn{
		regularCh: make(chan *Frame, 1),
		controlCh: make(chan *Frame, 1),
		closeCh:   make(chan struct{}),
	}
	c.regularCh <- &Frame{Type: MessageRegular, ID: 1}

	done := make(chan error, 1)
	go func() {
		done <- c.deliverFrame(&Frame{Type: MessageRegular, ID: 2})
	}()

	close(c.closeCh)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected deliverFrame to stop on close")
		}
	case <-time.After(time.Second):
		t.Fatal("deliverFrame did not stop when the connection closed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/protocol -run 'TestDeliverFrame' -count=1`
Expected: FAIL because `Conn.deliverFrame` does not exist and the current code drops frames with `default`.

- [ ] **Step 3: Write minimal implementation**

Update `internal/protocol/conn.go` so read-loop dispatch always waits for channel capacity or connection shutdown instead of silently dropping frames:

```go
type Conn struct {
	ws *websocket.Conn

	outgoingID  atomic.Uint32
	incomingAck atomic.Uint32

	mu        sync.Mutex
	closeOnce sync.Once
	closed    bool
	closeCh   chan struct{}
	debug     bool

	regularCh chan *Frame
	controlCh chan *Frame
}

func (c *Conn) signalClosed() {
	c.closeOnce.Do(func() {
		close(c.closeCh)
	})
}

func (c *Conn) deliverFrame(frame *Frame) error {
	switch frame.Type {
	case MessageRegular:
		c.incomingAck.Store(frame.ID)
		select {
		case c.regularCh <- frame:
			return nil
		case <-c.closeCh:
			return fmt.Errorf("connection closed")
		}
	case MessageControl:
		select {
		case c.controlCh <- frame:
			return nil
		case <-c.closeCh:
			return fmt.Errorf("connection closed")
		}
	default:
		return nil
	}
}
```

Then replace the `switch frame.Type { ... default: }` block inside `readLoop()` with:

```go
if err := c.deliverFrame(frame); err != nil {
	return
}
```

Also make both `readLoop()` and `Close()` call `signalClosed()` so blocked senders unblock during shutdown.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/protocol -run 'TestDeliverFrame' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/protocol/conn.go internal/protocol/conn_test.go
git commit -m "fix: stop dropping websocket protocol frames"
```

### Task 2: Add Shared Metadata Cache with Singleflight and Invalidation

**Files:**
- Modify: `internal/remotefs/fs.go`
- Create: `internal/remotefs/cachedfs.go`
- Create: `internal/remotefs/cachedfs_test.go`
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write the failing tests**

Create `internal/remotefs/cachedfs_test.go` with cache-hit, request-deduplication, and invalidation coverage:

```go
package remotefs

import (
	"sync"
	"testing"
	"time"
)

func TestCachedFSStatCachesAndDeduplicatesConcurrentCalls(t *testing.T) {
	remote := newCountingRemoteFS()
	cached := NewCachedFS(remote, CacheConfig{
		StatTTL:        time.Minute,
		DirTTL:         time.Minute,
		StatMaxEntries: 16,
		DirMaxEntries:  16,
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cached.Stat("/workspace/demo/main.go"); err != nil {
				t.Errorf("Stat() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if got := remote.StatCalls("/workspace/demo/main.go"); got != 1 {
		t.Fatalf("remote Stat() calls = %d, want 1", got)
	}
}

func TestCachedFSApplyChangesInvalidatesParentDir(t *testing.T) {
	remote := newCountingRemoteFS()
	cached := NewCachedFS(remote, DefaultCacheConfig())

	if _, err := cached.ReadDir("/workspace/demo"); err != nil {
		t.Fatalf("first ReadDir() error = %v", err)
	}
	if _, err := cached.ReadDir("/workspace/demo"); err != nil {
		t.Fatalf("second ReadDir() error = %v", err)
	}
	if got := remote.ReadDirCalls("/workspace/demo"); got != 1 {
		t.Fatalf("remote ReadDir() calls = %d, want 1", got)
	}

	cached.applyChanges([]FileChange{{
		Type:     FileChangeUpdated,
		Resource: "/workspace/demo/main.go",
	}})

	if _, err := cached.ReadDir("/workspace/demo"); err != nil {
		t.Fatalf("third ReadDir() error = %v", err)
	}
	if got := remote.ReadDirCalls("/workspace/demo"); got != 2 {
		t.Fatalf("remote ReadDir() calls after invalidation = %d, want 2", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/remotefs -run 'TestCachedFS' -count=1`
Expected: FAIL because `NewCachedFS`, `CacheConfig`, `DefaultCacheConfig`, and `applyChanges` do not exist.

- [ ] **Step 3: Write minimal implementation**

First add `singleflight`:

```bash
go get golang.org/x/sync@latest
```

Then update `internal/remotefs/fs.go` to expose watch support alongside the existing `FS` interface:

```go
type Watcher interface {
	Watch(sessionID, reqID, path string, recursive bool, handler func([]FileChange)) (func(), error)
}

type CacheConfig struct {
	StatTTL        time.Duration
	DirTTL         time.Duration
	StatMaxEntries int
	DirMaxEntries  int
}

func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		StatTTL:        30 * time.Second,
		DirTTL:         15 * time.Second,
		StatMaxEntries: 32768,
		DirMaxEntries:  4096,
	}
}
```

Create `internal/remotefs/cachedfs.go` with a wrapper that implements `FS`, caches `Stat` and `ReadDir`, and invalidates on local writes and remote file-change events:

```go
type CachedFS struct {
	base      FS
	watcher   Watcher
	statCache *cache.Cache
	dirCache  *cache.Cache
	statGroup singleflight.Group
	dirGroup  singleflight.Group
	stopWatch func()
}

func NewCachedFS(base FS, cfg CacheConfig) *CachedFS {
	watcher, _ := base.(Watcher)
	return &CachedFS{
		base:      base,
		watcher:   watcher,
		statCache: cache.New(cfg.StatTTL, cfg.StatMaxEntries),
		dirCache:  cache.New(cfg.DirTTL, cfg.DirMaxEntries),
		stopWatch: func() {},
	}
}

func (fs *CachedFS) Stat(path string) (*FileStat, error) {
	if v, ok := fs.statCache.Get(path); ok {
		return cloneFileStat(v.(*FileStat)), nil
	}
	v, err, _ := fs.statGroup.Do(path, func() (interface{}, error) {
		st, err := fs.base.Stat(path)
		if err != nil {
			return nil, err
		}
		fs.statCache.Set(path, cloneFileStat(st))
		return cloneFileStat(st), nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*FileStat), nil
}

func (fs *CachedFS) applyChanges(changes []FileChange) {
	for _, change := range changes {
		fs.statCache.Invalidate(change.Resource)
		fs.statCache.InvalidatePrefix(change.Resource + "/")
		fs.dirCache.Invalidate(change.Resource)
		fs.dirCache.Invalidate(path.Dir(change.Resource))
		fs.dirCache.InvalidatePrefix(change.Resource + "/")
	}
}
```

Also implement pass-through `ReadFile`, `WriteFile`, `Mkdir`, `Delete`, and `Rename` methods that invalidate the same path and parent-directory entries on success, plus `ReadDir`, `StartWatch(path string) (func(), error)`, and `Close() error`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/remotefs -run 'TestCachedFS' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/remotefs/fs.go internal/remotefs/cachedfs.go internal/remotefs/cachedfs_test.go
git commit -m "feat: add cached remote filesystem wrapper"
```

### Task 3: Wire CachedFS and Root Watch into the CLI Flow

**Files:**
- Modify: `cmd/code-local/main.go`
- Modify: `cmd/code-local/main_test.go`

- [ ] **Step 1: Write the failing tests**

Extend `cmd/code-local/main_test.go` with a watchable fake and a wrapper test:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/code-local -run 'TestWrapRemoteFS' -count=1`
Expected: FAIL because `wrapRemoteFS` does not exist.

- [ ] **Step 3: Write minimal implementation**

Add a helper in `cmd/code-local/main.go` that centralizes the remote wrapper logic before backend creation:

```go
func wrapRemoteFS(remote remotefs.FS, remotePath string) (remotefs.FS, func(), error) {
	cached := remotefs.NewCachedFS(remote, remotefs.DefaultCacheConfig())
	stop, err := cached.StartWatch(remotePath)
	if err != nil {
		return nil, nil, fmt.Errorf("start remote watch: %w", err)
	}
	return cached, stop, nil
}
```

Then replace the direct `remotefs.NewClient(ipc)` usage in `run()` with:

```go
	baseRemote := remotefs.NewClient(ipc)
	remote, stopRemote, err := wrapRemoteFS(baseRemote, remotePath)
	if err != nil {
		return fmt.Errorf("prepare remote filesystem: %w", err)
	}
	defer stopRemote()
```

Leave the backend constructors unchanged except for receiving the wrapped `remotefs.FS` instance.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/code-local -run 'TestWrapRemoteFS' -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/code-local/main.go cmd/code-local/main_test.go
git commit -m "feat: wrap mounted remote fs with shared cache"
```

### Task 4: Make NFS Attribute Caching Configurable and Document It

**Files:**
- Modify: `internal/nfs/server.go`
- Create: `internal/nfs/server_test.go`
- Modify: `cmd/code-local/main.go`
- Modify: `cmd/code-local/main_test.go`
- Modify: `README.md`
- Modify: `docs/usage.md`
- Modify: `docs/large-project-performance.md`

- [ ] **Step 1: Write the failing tests**

Create `internal/nfs/server_test.go` and extend the CLI tests:

```go
package nfs

import (
	"net"
	"strings"
	"testing"
)

func TestMountCmdIncludesConfiguredActimeo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	server := &Server{listener: ln, opts: Options{Actimeo: 30}}
	cmd := server.MountCmd("/tmp/code-local")
	if !strings.Contains(cmd, "actimeo=30") {
		t.Fatalf("mount command %q does not contain actimeo=30", cmd)
	}
}
```

Add this test to `cmd/code-local/main_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/code-local ./internal/nfs -run 'Test(MountCmdIncludesConfiguredActimeo|CreateBackendPassesNFSOptions)' -count=1`
Expected: FAIL because `Options` does not exist and `createBackend` does not pass NFS tuning through.

- [ ] **Step 3: Write minimal implementation**

Update `internal/nfs/server.go` to take explicit options:

```go
type Options struct {
	Actimeo int
}

type Server struct {
	listener net.Listener
	fs       *FileSystem
	opts     Options
}

func NewServer(remote remotefs.FS, remotePath string, port int, opts Options) (*Server, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return &Server{listener: listener, fs: NewFileSystem(remote, remotePath), opts: opts}, nil
}

func (s *Server) MountCmd(mountPoint string) string {
	_, port, _ := net.SplitHostPort(s.Addr())
	actimeo := s.opts.Actimeo
	if actimeo <= 0 {
		actimeo = 3
	}
	lockOpt := "nolock"
	if runtime.GOOS == "darwin" {
		lockOpt = "nolocks"
	}
	return fmt.Sprintf("sudo mount -t nfs -o port=%s,mountport=%s,vers=3,tcp,%s,actimeo=%d 127.0.0.1:/ %s", port, port, lockOpt, actimeo, mountPoint)
}
```

Then update `cmd/code-local/main.go` to parse and pass the new flag:

```go
	nfsActimeo := flag.Int("nfs-actimeo", 3, "NFS attribute cache timeout in seconds")

	if *nfsActimeo < 0 {
		return fmt.Errorf("nfs-actimeo must be >= 0")
	}

	server, err := createBackend(ctx, backend, remote, remotePath, port, nfsserver.Options{
		Actimeo: *nfsActimeo,
	})
```

Update `createBackend(...)` and the existing tests to accept the extra `nfsserver.Options` parameter.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/code-local ./internal/nfs -run 'Test(MountCmdIncludesConfiguredActimeo|CreateBackendPassesNFSOptions)' -count=1`
Expected: PASS

- [ ] **Step 5: Update the docs**

Apply these documentation changes:

```md
<!-- README.md -->
- `nfs`: recommended for large repositories because metadata caching is stronger than the current WebDAV path.
- `webdav`: keep as the compatibility backend when NFS tooling is unavailable.
```

```bash
./code-local \
  --url https://your-server:8080 \
  --password yourpass \
  --mount /tmp/remote \
  --backend nfs \
  --nfs-actimeo 30 \
  --remote-path /home/user/project
```

```md
<!-- docs/usage.md -->
| `--nfs-actimeo` | 否 | `3` | NFS attribute cache 秒数，大项目建议从 `30` 开始调优 |

- 大项目优先使用 `nfs`
- 大目录、读多写少的仓库可以提高 `--nfs-actimeo`
```

```md
<!-- docs/large-project-performance.md -->
详细实施计划见：`docs/superpowers/plans/2026-04-14-large-project-performance.md`
```

- [ ] **Step 6: Commit**

```bash
git add cmd/code-local/main.go cmd/code-local/main_test.go internal/nfs/server.go internal/nfs/server_test.go README.md docs/usage.md docs/large-project-performance.md
git commit -m "feat: add nfs attribute cache tuning"
```

### Task 5: Final Verification

**Files:**
- No new files; verification only

- [ ] **Step 1: Run targeted protocol and cache tests**

Run: `go test ./internal/protocol -run 'TestDeliverFrame' -count=1`
Run: `go test ./internal/remotefs -run 'TestCachedFS' -count=1`
Expected: PASS

- [ ] **Step 2: Run backend and CLI package tests**

Run: `go test ./cmd/code-local ./internal/nfs ./internal/webdav -count=1`
Expected: PASS

- [ ] **Step 3: Run the full package set that excludes reference fixtures**

Run: `go test ./cmd/code-local ./internal/auth ./internal/cache ./internal/nfs ./internal/protocol ./internal/remotefs ./internal/webdav -count=1`
Expected: PASS

- [ ] **Step 4: Manual smoke test against a real remote**

Run:

```bash
go build -o code-local ./cmd/code-local
./code-local --url https://your-server:8080 --password yourpass --mount /tmp/remote --backend nfs --nfs-actimeo 30 --remote-path /home/user/project
```

Expected:

- startup succeeds
- printed mount command contains `actimeo=30`
- repeated `ls /tmp/remote` calls stop triggering obvious warm-up stalls after the first pass

- [ ] **Step 5: Commit verification-only follow-ups if needed**

```bash
git status
```

If verification exposed no new changes: no commit.
If verification required small fixes: create a new commit instead of amending earlier ones.

## Scope Notes

- This plan intentionally does **not** redesign `readFile` / `writeFile` into chunked I/O. That is a separate follow-up once metadata-path latency is stabilized.
- This plan keeps caching above the backend boundary so NFS and WebDAV benefit together.
- Do **not** run `go test ./...` as the primary verification command in this repository; the `references/code-server` fixture tree is not a clean Go package set.
