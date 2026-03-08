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
	Artifact     model.CompiledArtifact
	CompileLog   string
	Language     model.Language
	artifactPath string
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
		_ = os.Remove(value.artifactPath)
	})
	if err != nil {
		return nil, fmt.Errorf("create LRU cache: %w", err)
	}

	return &CompileCache{
		cache:    cache,
		cacheDir: cacheDir,
	}, nil
}

// Get retrieves a cached artifact by key.
func (c *CompileCache) Get(key string) (*CachedArtifact, bool) {
	cached, ok := c.cache.Get(key)
	if !ok {
		return nil, false
	}

	data, err := os.ReadFile(cached.artifactPath)
	if err != nil {
		return nil, false
	}

	return &CachedArtifact{
		Artifact: model.CompiledArtifact{
			Name: cached.Artifact.Name,
			Data: data,
			Mode: cached.Artifact.Mode,
		},
		CompileLog: cached.CompileLog,
		Language:   cached.Language,
	}, true
}

// Put stores a compilation artifact in cache.
func (c *CompileCache) Put(key string, artifact model.CompiledArtifact, compileLog string, lang model.Language) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	cachedPath := filepath.Join(c.cacheDir, key)

	mode := artifact.Mode
	if mode == 0 {
		mode = 0o644
	}

	if err := os.WriteFile(cachedPath, artifact.Data, mode); err != nil {
		return fmt.Errorf("write cached artifact: %w", err)
	}

	cached := &CachedArtifact{
		Artifact: model.CompiledArtifact{
			Name: artifact.Name,
			Mode: mode,
		},
		CompileLog:   compileLog,
		Language:     lang,
		artifactPath: cachedPath,
	}

	c.cache.Add(key, cached)
	return nil
}

// Stats returns cache statistics.
func (c *CompileCache) Stats() Stats {
	return Stats{
		Entries: c.cache.Len(),
	}
}
