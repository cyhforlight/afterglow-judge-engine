package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheStorage_StoreWithKey_Get(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := NewCacheStorage(cacheDir, 10)
	require.NoError(t, err)

	ctx := context.Background()
	key := "0123456789abcdef0123456789abcdef"
	content := []byte("test content")

	// Store with key
	err = cache.StoreWithKey(ctx, key, content)
	require.NoError(t, err)

	// Get should return same content
	retrieved, err := cache.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, content, retrieved)

	// Verify file exists on disk
	filePath := filepath.Join(cacheDir, key)
	diskData, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, content, diskData)
}

func TestCacheStorage_Get_CacheMiss(t *testing.T) {
	cache, err := NewCacheStorage("", 10)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = cache.Get(ctx, "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cache miss")
}

func TestCacheStorage_LRU_Eviction(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := NewCacheStorage(cacheDir, 3) // Max 3 entries
	require.NoError(t, err)

	ctx := context.Background()

	// Add 3 entries
	for i := range 3 {
		key := fmt.Sprintf("key%d", i)
		content := fmt.Appendf(nil, "content%d", i)
		err := cache.StoreWithKey(ctx, key, content)
		require.NoError(t, err)
	}

	// All 3 should be in cache
	for i := range 3 {
		key := fmt.Sprintf("key%d", i)
		_, err := cache.Get(ctx, key)
		require.NoError(t, err)
	}

	// Add 4th entry, should evict oldest (key0)
	err = cache.StoreWithKey(ctx, "key3", []byte("content3"))
	require.NoError(t, err)

	// key0 should be evicted
	_, err = cache.Get(ctx, "key0")
	require.Error(t, err)

	// key1, key2, key3 should still be there
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("key%d", i)
		_, err := cache.Get(ctx, key)
		require.NoError(t, err)
	}

	// Verify evicted file is deleted from disk
	evictedPath := filepath.Join(cacheDir, "key0")
	_, err = os.Stat(evictedPath)
	assert.True(t, os.IsNotExist(err))
}

func TestCacheStorage_LRU_RecentlyAccessed(t *testing.T) {
	cache, err := NewCacheStorage("", 3)
	require.NoError(t, err)

	ctx := context.Background()

	// Add 3 entries
	for i := range 3 {
		key := fmt.Sprintf("key%d", i)
		content := fmt.Appendf(nil, "content%d", i)
		err := cache.StoreWithKey(ctx, key, content)
		require.NoError(t, err)
	}

	// Access key0 to make it recently used
	_, err = cache.Get(ctx, "key0")
	require.NoError(t, err)

	// Add 4th entry, should evict key1 (oldest unused)
	err = cache.StoreWithKey(ctx, "key3", []byte("content3"))
	require.NoError(t, err)

	// key0 should still be there (recently accessed)
	_, err = cache.Get(ctx, "key0")
	require.NoError(t, err)

	// key1 should be evicted
	_, err = cache.Get(ctx, "key1")
	require.Error(t, err)
}

func TestCacheStorage_Store_NotSupported(t *testing.T) {
	cache, err := NewCacheStorage("", 10)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = cache.Store(ctx, "test", []byte("content"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deterministic keys")
}

func TestCacheStorage_Delete(t *testing.T) {
	cacheDir := t.TempDir()
	cache, err := NewCacheStorage(cacheDir, 10)
	require.NoError(t, err)

	ctx := context.Background()
	key := "testkey"

	// Store
	err = cache.StoreWithKey(ctx, key, []byte("content"))
	require.NoError(t, err)

	// Delete
	err = cache.Delete(ctx, key)
	require.NoError(t, err)

	// Should be gone from cache
	_, err = cache.Get(ctx, key)
	require.Error(t, err)

	// Should be gone from disk
	filePath := filepath.Join(cacheDir, key)
	_, err = os.Stat(filePath)
	assert.True(t, os.IsNotExist(err))
}

func TestCacheStorage_Stats(t *testing.T) {
	cache, err := NewCacheStorage("", 10)
	require.NoError(t, err)

	ctx := context.Background()

	// Initially empty
	stats := cache.Stats()
	assert.Equal(t, 0, stats.Entries)

	// Add entries
	for i := range 5 {
		key := fmt.Sprintf("key%d", i)
		err := cache.StoreWithKey(ctx, key, []byte("content"))
		require.NoError(t, err)
	}

	stats = cache.Stats()
	assert.Equal(t, 5, stats.Entries)
}

func TestCacheStorage_Concurrent(t *testing.T) {
	cache, err := NewCacheStorage("", 100)
	require.NoError(t, err)

	ctx := context.Background()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key%d", n)
			content := fmt.Appendf(nil, "content%d", n)
			_ = cache.StoreWithKey(ctx, key, content)
		}(i)
	}

	// Concurrent reads
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key%d", n)
			_, _ = cache.Get(ctx, key)
		}(i)
	}

	wg.Wait()
}

func TestCacheStorage_OrphanCleanup(t *testing.T) {
	cacheDir := t.TempDir()

	// Create orphan files
	orphanFiles := []string{"orphan1", "orphan2", "orphan3"}
	for _, name := range orphanFiles {
		filePath := filepath.Join(cacheDir, name)
		err := os.WriteFile(filePath, []byte("orphan"), 0o644)
		require.NoError(t, err)
	}

	// Create cache - should clean up orphans
	_, err := NewCacheStorage(cacheDir, 10)
	require.NoError(t, err)

	// Verify orphans are deleted
	for _, name := range orphanFiles {
		filePath := filepath.Join(cacheDir, name)
		_, err := os.Stat(filePath)
		assert.True(t, os.IsNotExist(err))
	}
}
