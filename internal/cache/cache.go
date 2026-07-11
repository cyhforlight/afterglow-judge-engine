// Package cache provides a pure in-memory LRU cache.
package cache

import (
	"fmt"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Cache is a generic in-memory LRU cache.
// The underlying LRU implementation is already thread-safe.
type Cache[V any] struct {
	cache *lru.Cache[string, V]
}

// New creates a new cache with the specified maximum number of entries.
func New[V any](maxEntries int) (*Cache[V], error) {
	cache, err := lru.New[string, V](maxEntries)
	if err != nil {
		return nil, fmt.Errorf("create LRU cache: %w", err)
	}

	return &Cache[V]{
		cache: cache,
	}, nil
}

// Get retrieves cached content by key.
// Returns (value, true) if found, (zero, false) if not found.
func (c *Cache[V]) Get(key string) (V, bool) {
	return c.cache.Get(key)
}

// Set stores content with the given key.
func (c *Cache[V]) Set(key string, value V) {
	c.cache.Add(key, value)
}
