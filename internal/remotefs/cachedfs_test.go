package remotefs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingRemoteFS tracks per-path call counts for Stat and ReadDir.
type countingRemoteFS struct {
	mu           sync.Mutex
	statCounts   map[string]int
	readDirCounts map[string]int
}

func newCountingRemoteFS() *countingRemoteFS {
	return &countingRemoteFS{
		statCounts:    make(map[string]int),
		readDirCounts: make(map[string]int),
	}
}

func (f *countingRemoteFS) Stat(path string) (*FileStat, error) {
	f.mu.Lock()
	f.statCounts[path]++
	f.mu.Unlock()
	return &FileStat{Type: FileTypeFile, Size: 100}, nil
}

func (f *countingRemoteFS) StatCalls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statCounts[path]
}

func (f *countingRemoteFS) ReadDir(path string) ([]DirEntry, error) {
	f.mu.Lock()
	f.readDirCounts[path]++
	f.mu.Unlock()
	return []DirEntry{{Name: "main.go", Type: FileTypeFile}}, nil
}

func (f *countingRemoteFS) ReadDirCalls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readDirCounts[path]
}

func (f *countingRemoteFS) ReadFile(string) ([]byte, error)                       { return nil, nil }
func (f *countingRemoteFS) WriteFile(string, []byte, bool, bool) error            { return nil }
func (f *countingRemoteFS) Mkdir(string) error                                    { return nil }
func (f *countingRemoteFS) Delete(string, bool) error                             { return nil }
func (f *countingRemoteFS) Rename(string, string, bool) error                     { return nil }

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

func TestCachedFSWriteInvalidatesCache(t *testing.T) {
	remote := newCountingRemoteFS()
	cached := NewCachedFS(remote, DefaultCacheConfig())

	// Populate cache
	if _, err := cached.Stat("/workspace/demo/main.go"); err != nil {
		t.Fatal(err)
	}
	if got := remote.StatCalls("/workspace/demo/main.go"); got != 1 {
		t.Fatalf("initial stat calls = %d, want 1", got)
	}

	// Write should invalidate
	if err := cached.WriteFile("/workspace/demo/main.go", []byte("x"), false, true); err != nil {
		t.Fatal(err)
	}

	// Next stat should go to remote again
	if _, err := cached.Stat("/workspace/demo/main.go"); err != nil {
		t.Fatal(err)
	}
	if got := remote.StatCalls("/workspace/demo/main.go"); got != 2 {
		t.Fatalf("stat calls after write = %d, want 2", got)
	}
}

func TestCachedFSSingleflightDeduplicates(t *testing.T) {
	var callCount atomic.Int32
	slow := &slowStatFS{callCount: &callCount}
	cached := NewCachedFS(slow, CacheConfig{
		StatTTL:        time.Minute,
		DirTTL:         time.Minute,
		StatMaxEntries: 16,
		DirMaxEntries:  16,
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cached.Stat("/slow")
		}()
	}
	wg.Wait()

	if got := callCount.Load(); got != 1 {
		t.Fatalf("slow Stat() calls = %d, want 1", got)
	}
}

type slowStatFS struct {
	countingRemoteFS
	callCount *atomic.Int32
}

func (f *slowStatFS) Stat(path string) (*FileStat, error) {
	f.callCount.Add(1)
	time.Sleep(50 * time.Millisecond)
	return &FileStat{Type: FileTypeFile, Size: 1}, nil
}
