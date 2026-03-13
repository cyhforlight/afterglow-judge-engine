package cache

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_Set_Get(t *testing.T) {
	cache, err := New(10)
	require.NoError(t, err)

	key := "0123456789abcdef0123456789abcdef"
	content := []byte("test content")

	// Set
	cache.Set(key, content)

	// Get should return same content
	retrieved, ok := cache.Get(key)
	require.True(t, ok)
	assert.Equal(t, content, retrieved)
}

func TestCache_Get_Miss(t *testing.T) {
	cache, err := New(10)
	require.NoError(t, err)

	_, ok := cache.Get("nonexistent")
	assert.False(t, ok)
}

func TestCache_LRU_Eviction(t *testing.T) {
	cache, err := New(3) // Max 3 entries
	require.NoError(t, err)

	// Add 3 entries
	for i := range 3 {
		key := fmt.Sprintf("key%d", i)
		content := fmt.Appendf(nil, "content%d", i)
		cache.Set(key, content)
	}

	// All 3 should be in cache
	for i := range 3 {
		key := fmt.Sprintf("key%d", i)
		_, ok := cache.Get(key)
		require.True(t, ok)
	}

	// Add 4th entry, should evict oldest (key0)
	cache.Set("key3", []byte("content3"))

	// key0 should be evicted
	_, ok := cache.Get("key0")
	assert.False(t, ok)

	// key1, key2, key3 should still be there
	for i := 1; i <= 3; i++ {
		key := fmt.Sprintf("key%d", i)
		_, ok := cache.Get(key)
		require.True(t, ok)
	}
}

func TestCache_LRU_RecentlyAccessed(t *testing.T) {
	cache, err := New(3)
	require.NoError(t, err)

	// Add 3 entries
	for i := range 3 {
		key := fmt.Sprintf("key%d", i)
		content := fmt.Appendf(nil, "content%d", i)
		cache.Set(key, content)
	}

	// Access key0 to make it recently used
	_, ok := cache.Get("key0")
	require.True(t, ok)

	// Add 4th entry, should evict key1 (oldest unused)
	cache.Set("key3", []byte("content3"))

	// key0 should still be there (recently accessed)
	_, ok = cache.Get("key0")
	require.True(t, ok)

	// key1 should be evicted
	_, ok = cache.Get("key1")
	assert.False(t, ok)
}

func TestCache_Len(t *testing.T) {
	cache, err := New(10)
	require.NoError(t, err)

	// Initially empty
	assert.Equal(t, 0, cache.Len())

	// Add entries
	for i := range 5 {
		key := fmt.Sprintf("key%d", i)
		cache.Set(key, []byte("content"))
	}

	assert.Equal(t, 5, cache.Len())
}

func TestCache_Concurrent(t *testing.T) {
	cache, err := New(100)
	require.NoError(t, err)

	var wg sync.WaitGroup

	// Concurrent writes
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key%d", n)
			content := fmt.Appendf(nil, "content%d", n)
			cache.Set(key, content)
		}(i)
	}

	// Concurrent reads
	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("key%d", n)
			_, _ = cache.Get(key)
		}(i)
	}

	wg.Wait()
}
