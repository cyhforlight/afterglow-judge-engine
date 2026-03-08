package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalStorage_Get(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test resource
	resourcePath := filepath.Join(tmpDir, "test-resource.txt")
	content := []byte("test resource content")
	err := os.WriteFile(resourcePath, content, 0o644)
	require.NoError(t, err)

	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	// Get existing resource
	data, err := storage.Get(ctx, "test-resource.txt")
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestInternalStorage_Get_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = storage.Get(ctx, "nonexistent.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestInternalStorage_Store_ReadOnly(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = storage.Store(ctx, "test", []byte("content"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
}

func TestInternalStorage_StoreWithKey_ReadOnly(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	err = storage.StoreWithKey(ctx, "key", []byte("content"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
}

func TestInternalStorage_Delete_ReadOnly(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	err = storage.Delete(ctx, "key")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read-only")
}
