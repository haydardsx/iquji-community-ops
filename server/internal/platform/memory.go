package platform

import (
	"sync"
	"time"
)

type Cache struct {
	mu    sync.RWMutex
	items map[string]cacheItem
}

type cacheItem struct {
	value     any
	expiresAt time.Time
}

func NewCache() *Cache {
	return &Cache{items: map[string]cacheItem{}}
}

func (c *Cache) Set(key string, value any, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = cacheItem{value: value, expiresAt: time.Now().Add(ttl)}
}

func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(item.expiresAt) {
		if ok {
			c.mu.Lock()
			delete(c.items, key)
			c.mu.Unlock()
		}
		return nil, false
	}
	return item.value, true
}

func (c *Cache) Take(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return nil, false
	}
	delete(c.items, key)
	if time.Now().After(item.expiresAt) {
		return nil, false
	}
	return item.value, true
}

func (c *Cache) Clear() {
	c.mu.Lock()
	c.items = map[string]cacheItem{}
	c.mu.Unlock()
}
