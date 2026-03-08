package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"afterglow-judge-sandbox/internal/model"
)

func TestCompileCache_PutAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCompileCache(tmpDir, 10)
	require.NoError(t, err)

	err = cache.Put("key1", createCompiledArtifact("program", "binary content"), "compile log", model.LanguageC)
	require.NoError(t, err)

	// Get artifact from cache
	cached, ok := cache.Get("key1")
	require.True(t, ok, "cache should contain key1")
	assert.Equal(t, "compile log", cached.CompileLog)
	assert.Equal(t, model.LanguageC, cached.Language)
	assert.Equal(t, "program", cached.Artifact.Name)
	assert.Equal(t, []byte("binary content"), cached.Artifact.Data)
	assert.Equal(t, os.FileMode(0o755), cached.Artifact.Mode)
}

func TestCompileCache_MissOnNonexistent(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCompileCache(tmpDir, 10)
	require.NoError(t, err)

	_, ok := cache.Get("nonexistent")
	assert.False(t, ok, "cache should not contain nonexistent key")
}

func TestCompileCache_EvictionRemovesFile(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCompileCache(tmpDir, 2) // max 2 entries
	require.NoError(t, err)

	err = cache.Put("key1", createCompiledArtifact("program1", "binary1"), "log1", model.LanguageC)
	require.NoError(t, err)
	err = cache.Put("key2", createCompiledArtifact("program2", "binary2"), "log2", model.LanguageC)
	require.NoError(t, err)
	err = cache.Put("key3", createCompiledArtifact("program3", "binary3"), "log3", model.LanguageC)
	require.NoError(t, err)

	// Verify key1 was evicted
	_, ok := cache.Get("key1")
	assert.False(t, ok, "key1 should be evicted")

	// Verify key1's disk file was deleted
	cachedPath1 := filepath.Join(tmpDir, "key1")
	_, err = os.Stat(cachedPath1)
	assert.True(t, os.IsNotExist(err), "evicted file should be deleted")

	// Verify key2 and key3 still exist
	_, ok = cache.Get("key2")
	assert.True(t, ok, "key2 should still exist")
	_, ok = cache.Get("key3")
	assert.True(t, ok, "key3 should still exist")
}

func TestCompileCache_LRUOrdering(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCompileCache(tmpDir, 2)
	require.NoError(t, err)

	require.NoError(t, cache.Put("key1", createCompiledArtifact("program1", "binary1"), "log1", model.LanguageC))
	require.NoError(t, cache.Put("key2", createCompiledArtifact("program2", "binary2"), "log2", model.LanguageC))

	// Access key1 to make it recently used
	cache.Get("key1")

	// Add key3, should evict key2 (least recently used)
	require.NoError(t, cache.Put("key3", createCompiledArtifact("program3", "binary3"), "log3", model.LanguageC))

	// Verify key2 was evicted, key1 and key3 remain
	_, ok := cache.Get("key2")
	assert.False(t, ok, "key2 should be evicted (LRU)")
	_, ok = cache.Get("key1")
	assert.True(t, ok, "key1 should remain (recently accessed)")
	_, ok = cache.Get("key3")
	assert.True(t, ok, "key3 should remain (just added)")
}

func TestCompileCache_Stats(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewCompileCache(tmpDir, 10)
	require.NoError(t, err)

	stats := cache.Stats()
	assert.Equal(t, 0, stats.Entries)

	require.NoError(t, cache.Put("key1", createCompiledArtifact("program", "binary"), "log", model.LanguageC))

	stats = cache.Stats()
	assert.Equal(t, 1, stats.Entries)
}

func TestCompileCache_OrphanCleanup(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some orphan files
	orphan1 := filepath.Join(tmpDir, "orphan1")
	orphan2 := filepath.Join(tmpDir, "orphan2")
	require.NoError(t, os.WriteFile(orphan1, []byte("old1"), 0644))
	require.NoError(t, os.WriteFile(orphan2, []byte("old2"), 0644))

	// Verify orphans exist
	_, err := os.Stat(orphan1)
	require.NoError(t, err)
	_, err = os.Stat(orphan2)
	require.NoError(t, err)

	// Create new cache (should clean up orphans)
	cache, err := NewCompileCache(tmpDir, 10)
	require.NoError(t, err)

	// Verify orphans are deleted
	_, err = os.Stat(orphan1)
	assert.True(t, os.IsNotExist(err), "orphan1 should be deleted")
	_, err = os.Stat(orphan2)
	assert.True(t, os.IsNotExist(err), "orphan2 should be deleted")

	// Verify cache is empty
	stats := cache.Stats()
	assert.Equal(t, 0, stats.Entries, "cache should start empty after cleanup")
}

func createCompiledArtifact(name, content string) model.CompiledArtifact {
	return model.CompiledArtifact{
		Name: name,
		Data: []byte(content),
		Mode: 0o755,
	}
}
