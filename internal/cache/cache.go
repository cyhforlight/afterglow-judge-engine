// Package cache provides a pure in-memory LRU cache.
package cache

import (
	"fmt"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Cache is a pure in-memory LRU cache.
// The underlying LRU implementation is already thread-safe.
type Cache struct {
	cache *lru.Cache[string, []byte]
}

// New creates a new cache with the specified maximum number of entries.
func New(maxEntries int) (*Cache, error) {
	cache, err := lru.New[string, []byte](maxEntries)
	if err != nil {
		return nil, fmt.Errorf("create LRU cache: %w", err)
	}

	return &Cache{
		cache: cache,
	}, nil
}

// Get retrieves cached content by key.
// Returns (value, true) if found, (nil, false) if not found.
func (c *Cache) Get(key string) ([]byte, bool) {
	return c.cache.Get(key)
}

// Set stores content with the given key.
func (c *Cache) Set(key string, value []byte) {
	c.cache.Add(key, value)
}

// Len returns the number of entries in the cache.
func (c *Cache) Len() int {
	return c.cache.Len()
}
