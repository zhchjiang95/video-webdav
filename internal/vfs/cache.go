package vfs

import (
	"sync"
	"time"
)

// boolCache 是带过期时间的简单缓存，用于记录“目录是否包含视频”。
type boolCache struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]cacheEntry
}

type cacheEntry struct {
	v   bool
	exp time.Time
}

// maxCacheEntries 缓存条目上限，超过后整体清空，避免内存无限增长
const maxCacheEntries = 200000

func newBoolCache(ttl time.Duration) *boolCache {
	return &boolCache{ttl: ttl, m: make(map[string]cacheEntry)}
}

func (c *boolCache) get(key string) (bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return false, false
	}
	if time.Now().After(e.exp) {
		delete(c.m, key)
		return false, false
	}
	return e.v, true
}

func (c *boolCache) set(key string, v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= maxCacheEntries {
		c.m = make(map[string]cacheEntry)
	}
	c.m[key] = cacheEntry{v: v, exp: time.Now().Add(c.ttl)}
}
