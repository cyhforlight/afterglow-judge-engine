package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

// CacheStorage implements Storage with LRU caching.
// Designed for system-generated temporary data (compilation artifacts).
type CacheStorage struct {
	cache    *lru.Cache[string, []byte]
	cacheDir string // Optional disk persistence
	mu       sync.RWMutex
}

// NewCacheStorage creates a cache storage with LRU eviction.
// If cacheDir is provided, files are persisted to disk for recovery after restart.
// Orphan files from previous runs are cleaned up on startup.
func NewCacheStorage(cacheDir string, maxEntries int) (*CacheStorage, error) {
	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, fmt.Errorf("create cache dir: %w", err)
		}

		// Clean up orphan files from previous runs
		entries, err := os.ReadDir(cacheDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					_ = os.Remove(filepath.Join(cacheDir, entry.Name()))
				}
			}
		}
	}

	cache, err := lru.NewWithEvict(maxEntries, func(key string, _ []byte) {
		// Eviction callback: delete disk file if exists
		if cacheDir != "" {
			_ = os.Remove(filepath.Join(cacheDir, key))
		}
	})
	if err != nil {
		return nil, fmt.Errorf("create LRU cache: %w", err)
	}

	return &CacheStorage{
		cache:    cache,
		cacheDir: cacheDir,
	}, nil
}

// StoreWithKey stores content with a deterministic key (content-addressable).
func (s *CacheStorage) StoreWithKey(_ context.Context, key string, content []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Persist to disk (optional, for recovery after restart)
	if s.cacheDir != "" {
		filePath := filepath.Join(s.cacheDir, key)
		if err := os.WriteFile(filePath, content, 0o644); err != nil {
			return fmt.Errorf("write to disk: %w", err)
		}
	}

	// Cache in memory
	s.cache.Add(key, content)
	return nil
}

// Get retrieves cached content by key.
func (s *CacheStorage) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, ok := s.cache.Get(key)
	if !ok {
		return nil, fmt.Errorf("cache miss: %s", key)
	}

	return data, nil
}

// Store is not supported (CacheStorage requires deterministic keys).
func (s *CacheStorage) Store(_ context.Context, _ string, _ []byte) (string, error) {
	return "", errors.New("CacheStorage requires deterministic keys, use StoreWithKey")
}

// Delete removes content from cache.
func (s *CacheStorage) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cache.Remove(key)

	if s.cacheDir != "" {
		filePath := filepath.Join(s.cacheDir, key)
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

// Stats contains cache statistics.
type Stats struct {
	Entries int
}

// Stats returns cache statistics.
func (s *CacheStorage) Stats() Stats {
	return Stats{
		Entries: s.cache.Len(),
	}
}
