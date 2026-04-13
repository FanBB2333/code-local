package remotefs

import (
	"fmt"
	"path"

	"github.com/FanBB2333/code-local/internal/cache"
	"golang.org/x/sync/singleflight"
)

// CachedFS wraps an FS with shared metadata caching and singleflight deduplication.
type CachedFS struct {
	base      FS
	watcher   Watcher
	statCache *cache.Cache
	dirCache  *cache.Cache
	statGroup singleflight.Group
	dirGroup  singleflight.Group
	stopWatch func()
}

// NewCachedFS creates a caching wrapper around base. If base implements Watcher,
// it will be used for watch-driven cache invalidation via StartWatch.
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

func cloneFileStat(st *FileStat) *FileStat {
	cp := *st
	return &cp
}

func (fs *CachedFS) Stat(p string) (*FileStat, error) {
	if v, ok := fs.statCache.Get(p); ok {
		return cloneFileStat(v.(*FileStat)), nil
	}
	v, err, _ := fs.statGroup.Do(p, func() (interface{}, error) {
		st, err := fs.base.Stat(p)
		if err != nil {
			return nil, err
		}
		fs.statCache.Set(p, cloneFileStat(st))
		return cloneFileStat(st), nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*FileStat), nil
}

func (fs *CachedFS) ReadDir(p string) ([]DirEntry, error) {
	if v, ok := fs.dirCache.Get(p); ok {
		return v.([]DirEntry), nil
	}
	v, err, _ := fs.dirGroup.Do(p, func() (interface{}, error) {
		entries, err := fs.base.ReadDir(p)
		if err != nil {
			return nil, err
		}
		fs.dirCache.Set(p, entries)
		return entries, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]DirEntry), nil
}

func (fs *CachedFS) ReadFile(p string) ([]byte, error) {
	return fs.base.ReadFile(p)
}

func (fs *CachedFS) WriteFile(p string, data []byte, create, overwrite bool) error {
	err := fs.base.WriteFile(p, data, create, overwrite)
	if err == nil {
		fs.invalidatePath(p)
	}
	return err
}

func (fs *CachedFS) Mkdir(p string) error {
	err := fs.base.Mkdir(p)
	if err == nil {
		fs.invalidatePath(p)
	}
	return err
}

func (fs *CachedFS) Delete(p string, recursive bool) error {
	err := fs.base.Delete(p, recursive)
	if err == nil {
		fs.invalidatePath(p)
	}
	return err
}

func (fs *CachedFS) Rename(oldPath, newPath string, overwrite bool) error {
	err := fs.base.Rename(oldPath, newPath, overwrite)
	if err == nil {
		fs.invalidatePath(oldPath)
		fs.invalidatePath(newPath)
	}
	return err
}

func (fs *CachedFS) invalidatePath(p string) {
	fs.statCache.Invalidate(p)
	fs.statCache.InvalidatePrefix(p + "/")
	fs.dirCache.Invalidate(p)
	fs.dirCache.Invalidate(path.Dir(p))
	fs.dirCache.InvalidatePrefix(p + "/")
}

func (fs *CachedFS) applyChanges(changes []FileChange) {
	for _, change := range changes {
		fs.invalidatePath(change.Resource)
	}
}

// StartWatch begins a recursive watch on the given path and wires file-change
// events into cache invalidation. Returns a stop function.
func (fs *CachedFS) StartWatch(watchPath string) (func(), error) {
	if fs.watcher == nil {
		return func() {}, nil
	}
	unsub, err := fs.watcher.Watch("cachedfs", "watch-0", watchPath, true, func(changes []FileChange) {
		fs.applyChanges(changes)
	})
	if err != nil {
		return nil, fmt.Errorf("start watch on %s: %w", watchPath, err)
	}
	fs.stopWatch = unsub
	return unsub, nil
}

// Close stops any active watch.
func (fs *CachedFS) Close() error {
	fs.stopWatch()
	return nil
}

var _ FS = (*CachedFS)(nil)
