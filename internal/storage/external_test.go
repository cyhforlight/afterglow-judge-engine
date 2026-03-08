package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalStorage_Store_Get(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewExternalStorage(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()
	content := []byte("test content")

	// Store
	key, err := storage.Store(ctx, "test.txt", content)
	require.NoError(t, err)
	assert.NotEmpty(t, key)

	// Get
	retrieved, err := storage.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, content, retrieved)
}

func TestExternalStorage_StoreWithKey_Get(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewExternalStorage(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()
	key := "0123456789abcdef"
	content := []byte("test content")

	// Store with key
	err = storage.StoreWithKey(ctx, key, content)
	require.NoError(t, err)

	// Get
	retrieved, err := storage.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, content, retrieved)
}

func TestExternalStorage_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewExternalStorage(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	// Store
	key, err := storage.Store(ctx, "test.txt", []byte("content"))
	require.NoError(t, err)

	// Delete
	err = storage.Delete(ctx, key)
	require.NoError(t, err)

	// Verify gone
	_, err = storage.Get(ctx, key)
	require.Error(t, err)
}
