package cache

import (
	"sync"
	"time"
)

// Entry is a cached value with expiration.
type Entry struct {
	Value     interface{}
	ExpiresAt time.Time
}

// Cache is a simple TTL + LRU-style cache for metadata and file content.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	ttl     time.Duration
	maxSize int
}

func New(ttl time.Duration, maxSize int) *Cache {
	return &Cache{
		entries: make(map[string]*Entry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.ExpiresAt) {
		return nil, false
	}
	return e.Value, true
}

func (c *Cache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict if over capacity (simple: remove oldest expired, then any)
	if len(c.entries) >= c.maxSize {
		c.evictOne()
	}

	c.entries[key] = &Entry{
		Value:     value,
		ExpiresAt: time.Now().Add(c.ttl),
	}
}

func (c *Cache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// InvalidatePrefix removes all entries matching a path prefix.
func (c *Cache) InvalidatePrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.entries, k)
		}
	}
}

func (c *Cache) evictOne() {
	var oldest string
	var oldestTime time.Time
	now := time.Now()

	for k, e := range c.entries {
		// Prefer removing expired entries
		if now.After(e.ExpiresAt) {
			delete(c.entries, k)
			return
		}
		if oldest == "" || e.ExpiresAt.Before(oldestTime) {
			oldest = k
			oldestTime = e.ExpiresAt
		}
	}
	if oldest != "" {
		delete(c.entries, oldest)
	}
}
