package remotefs

import "time"

// FS is the subset of remote filesystem operations required by local mount backends.
type FS interface {
	Stat(path string) (*FileStat, error)
	ReadDir(path string) ([]DirEntry, error)
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, create, overwrite bool) error
	Mkdir(path string) error
	Delete(path string, recursive bool) error
	Rename(oldPath, newPath string, overwrite bool) error
}

// Watcher is an optional interface for FS implementations that support file watching.
type Watcher interface {
	Watch(sessionID, reqID, path string, recursive bool, handler func([]FileChange)) (func(), error)
}

// CacheConfig controls the shared metadata cache behavior.
type CacheConfig struct {
	StatTTL        time.Duration
	DirTTL         time.Duration
	StatMaxEntries int
	DirMaxEntries  int
}

// DefaultCacheConfig returns production defaults for large project mounts.
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		StatTTL:        30 * time.Second,
		DirTTL:         15 * time.Second,
		StatMaxEntries: 32768,
		DirMaxEntries:  4096,
	}
}

var _ FS = (*Client)(nil)
