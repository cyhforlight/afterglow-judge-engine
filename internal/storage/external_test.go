package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-engine/internal/cache"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalStorage_Get(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("test content")
	err := os.WriteFile(testFile, content, 0o644)
	require.NoError(t, err)

	storage, err := NewExternalStorage(tmpDir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Get file
	retrieved, err := storage.Get(ctx, "test.txt")
	require.NoError(t, err)
	assert.Equal(t, content, retrieved)
}

func TestExternalStorage_Get_WithCache(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("test content")
	err := os.WriteFile(testFile, content, 0o644)
	require.NoError(t, err)

	testCache, err := cache.New(10)
	require.NoError(t, err)

	storage, err := NewExternalStorage(tmpDir, testCache)
	require.NoError(t, err)

	ctx := context.Background()

	// First read (cache miss)
	initialLen := testCache.Len()
	retrieved1, err := storage.Get(ctx, "test.txt")
	require.NoError(t, err)
	assert.Equal(t, content, retrieved1)
	assert.Equal(t, initialLen+1, testCache.Len(), "cache should have one new entry")

	// Second read (cache hit)
	retrieved2, err := storage.Get(ctx, "test.txt")
	require.NoError(t, err)
	assert.Equal(t, content, retrieved2)
	assert.Equal(t, initialLen+1, testCache.Len(), "cache size should not change on hit")
}

func TestExternalStorage_Get_CacheInvalidation(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	content1 := []byte("version 1")
	err := os.WriteFile(testFile, content1, 0o644)
	require.NoError(t, err)

	// Set initial mtime
	mtime1 := time.Unix(1000000000, 0)
	err = os.Chtimes(testFile, mtime1, mtime1)
	require.NoError(t, err)

	testCache, err := cache.New(10)
	require.NoError(t, err)

	storage, err := NewExternalStorage(tmpDir, testCache)
	require.NoError(t, err)

	ctx := context.Background()

	// First read (should cache with mtime1)
	retrieved1, err := storage.Get(ctx, "test.txt")
	require.NoError(t, err)
	assert.Equal(t, content1, retrieved1)
	assert.Equal(t, 1, testCache.Len(), "cache should have 1 entry")

	// Modify file content and mtime
	content2 := []byte("version 2")
	err = os.WriteFile(testFile, content2, 0o644)
	require.NoError(t, err)

	mtime2 := time.Unix(1000000001, 0)
	err = os.Chtimes(testFile, mtime2, mtime2)
	require.NoError(t, err)

	// Second read should get new content (different cache key due to mtime change)
	retrieved2, err := storage.Get(ctx, "test.txt")
	require.NoError(t, err)
	assert.Equal(t, content2, retrieved2, "should read new content after mtime change")
	assert.Equal(t, 2, testCache.Len(), "cache should have 2 entries (old + new, demonstrating zombie entries)")
}

func TestExternalStorage_Get_SubDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Create subdirectory and file
	subDir := filepath.Join(tmpDir, "testdata")
	err := os.MkdirAll(subDir, 0o755)
	require.NoError(t, err)

	testFile := filepath.Join(subDir, "input.txt")
	content := []byte("input data")
	err = os.WriteFile(testFile, content, 0o644)
	require.NoError(t, err)

	storage, err := NewExternalStorage(tmpDir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Get file with relative path
	retrieved, err := storage.Get(ctx, "testdata/input.txt")
	require.NoError(t, err)
	assert.Equal(t, content, retrieved)
}

func TestExternalStorage_Get_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	storage, err := NewExternalStorage(tmpDir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = storage.Get(ctx, "nonexistent.txt")
	require.Error(t, err)
	// Error message changed due to symlink resolution
	assert.Contains(t, err.Error(), "no such file")
}

func TestExternalStorage_Get_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	storage, err := NewExternalStorage(tmpDir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Try path traversal
	_, err = storage.Get(ctx, "../../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes base directory")
}

func TestExternalStorage_Get_SymlinkEscape_Blocked(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a symlink pointing outside mount point
	evilLink := filepath.Join(tmpDir, "evil.txt")
	err := os.Symlink("/etc/passwd", evilLink)
	require.NoError(t, err)

	storage, err := NewExternalStorage(tmpDir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Attempt to read symlink should be blocked
	_, err = storage.Get(ctx, "evil.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink escapes mount point")
}

func TestExternalStorage_Get_SymlinkWithinMount_Allowed(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a normal file
	targetFile := filepath.Join(tmpDir, "target.txt")
	content := []byte("target content")
	err := os.WriteFile(targetFile, content, 0o644)
	require.NoError(t, err)

	// Create a symlink pointing to file within mount point
	linkFile := filepath.Join(tmpDir, "link.txt")
	err = os.Symlink(targetFile, linkFile)
	require.NoError(t, err)

	storage, err := NewExternalStorage(tmpDir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Reading symlink within mount point should work
	retrieved, err := storage.Get(ctx, "link.txt")
	require.NoError(t, err)
	assert.Equal(t, content, retrieved)
}

func TestExternalStorage_Get_DotDotFilename_Allowed(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files with names starting with ".."
	testCases := []struct {
		name    string
		content []byte
	}{
		{"..hidden.txt", []byte("hidden file")},
		{"..config", []byte("config data")},
	}

	for _, tc := range testCases {
		filePath := filepath.Join(tmpDir, tc.name)
		err := os.WriteFile(filePath, tc.content, 0o644)
		require.NoError(t, err)
	}

	// Create a directory starting with ".."
	dotDotDir := filepath.Join(tmpDir, "..dir")
	err := os.MkdirAll(dotDotDir, 0o755)
	require.NoError(t, err)

	dotDotDirFile := filepath.Join(dotDotDir, "test.txt")
	dotDotDirContent := []byte("file in ..dir")
	err = os.WriteFile(dotDotDirFile, dotDotDirContent, 0o644)
	require.NoError(t, err)

	storage, err := NewExternalStorage(tmpDir, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Test files with ".." prefix should be accessible
	for _, tc := range testCases {
		retrieved, err := storage.Get(ctx, tc.name)
		require.NoError(t, err, "should allow file: %s", tc.name)
		assert.Equal(t, tc.content, retrieved)
	}

	// Test file in directory with ".." prefix
	retrieved, err := storage.Get(ctx, "..dir/test.txt")
	require.NoError(t, err, "should allow file in ..dir/")
	assert.Equal(t, dotDotDirContent, retrieved)
}
