// Package cache provides compilation artifact caching with LRU eviction.
package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"

	"afterglow-judge-sandbox/internal/model"
)

// CachedArtifact represents a cached compilation artifact.
type CachedArtifact struct {
	ArtifactPath string
	CompileLog   string
	Language     model.Language
}

// CompileCache manages cached compilation artifacts with LRU eviction.
type CompileCache struct {
	cache    *lru.Cache[string, *CachedArtifact]
	cacheDir string
	mu       sync.Mutex // protects file operations
}

// Stats contains cache statistics.
type Stats struct {
	Entries int
}

// NewCompileCache creates a compilation cache with entry limit.
// It cleans up orphan files from previous runs on startup.
func NewCompileCache(cacheDir string, maxEntries int) (*CompileCache, error) {
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
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

	cache, err := lru.NewWithEvict(maxEntries, func(_ string, value *CachedArtifact) {
		// Eviction callback: delete disk file
		_ = os.Remove(value.ArtifactPath)
	})
	if err != nil {
		return nil, fmt.Errorf("create LRU cache: %w", err)
	}

	return &CompileCache{
		cache:    cache,
		cacheDir: cacheDir,
	}, nil
}

// NewCompileCacheForTest creates an isolated cache instance for testing.
// This is now just an alias for NewCompileCache.
func NewCompileCacheForTest(cacheDir string, maxEntries int) (*CompileCache, error) {
	return NewCompileCache(cacheDir, maxEntries)
}

// Get retrieves a cached artifact by key.
func (c *CompileCache) Get(key string) (*CachedArtifact, bool) {
	return c.cache.Get(key)
}

// Put stores a compilation artifact in cache.
func (c *CompileCache) Put(key string, artifactPath string, compileLog string, lang model.Language) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Copy artifact to cache directory
	cachedPath := filepath.Join(c.cacheDir, key)
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return fmt.Errorf("read artifact: %w", err)
	}

	if err := os.WriteFile(cachedPath, data, 0644); err != nil {
		return fmt.Errorf("write cached artifact: %w", err)
	}

	artifact := &CachedArtifact{
		ArtifactPath: cachedPath,
		CompileLog:   compileLog,
		Language:     lang,
	}

	c.cache.Add(key, artifact)
	return nil
}

// Stats returns cache statistics.
func (c *CompileCache) Stats() Stats {
	return Stats{
		Entries: c.cache.Len(),
	}
}
