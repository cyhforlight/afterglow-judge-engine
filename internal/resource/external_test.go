package resource

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternal_Get_SeesFileUpdates(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	content1 := []byte("version 1")
	err := os.WriteFile(testFile, content1, 0o644)
	require.NoError(t, err)

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	retrieved1, err := ext.Get(ctx, "test.txt")
	require.NoError(t, err)
	assert.Equal(t, content1, retrieved1)

	content2 := []byte("version 2")
	err = os.WriteFile(testFile, content2, 0o644)
	require.NoError(t, err)

	retrieved2, err := ext.Get(ctx, "test.txt")
	require.NoError(t, err)
	assert.Equal(t, content2, retrieved2)
}

func TestExternal_Get_SubDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	subDir := filepath.Join(tmpDir, "testdata")
	err := os.MkdirAll(subDir, 0o755)
	require.NoError(t, err)

	testFile := filepath.Join(subDir, "input.txt")
	content := []byte("input data")
	err = os.WriteFile(testFile, content, 0o644)
	require.NoError(t, err)

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	retrieved, err := ext.Get(ctx, "testdata/input.txt")
	require.NoError(t, err)
	assert.Equal(t, content, retrieved)
}

func TestExternal_Get_DirectoryRejected(t *testing.T) {
	tmpDir := t.TempDir()

	subDir := filepath.Join(tmpDir, "cases")
	err := os.MkdirAll(subDir, 0o755)
	require.NoError(t, err)

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	_, err = ext.Get(context.Background(), "cases")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "regular file")
}

func TestExternal_Stat(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "test.txt")
	err := os.WriteFile(testFile, []byte("test content"), 0o644)
	require.NoError(t, err)

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	err = ext.Stat(context.Background(), "test.txt")
	require.NoError(t, err)

	err = ext.Stat(context.Background(), "missing.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file")
}

func TestExternal_Get_FileNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = ext.Get(ctx, "nonexistent.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file")
}

func TestExternal_Get_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = ext.Get(ctx, "../../../etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes base directory")
}

func TestExternal_Get_SymlinkEscape_Blocked(t *testing.T) {
	tmpDir := t.TempDir()

	evilLink := filepath.Join(tmpDir, "evil.txt")
	err := os.Symlink("/etc/passwd", evilLink)
	require.NoError(t, err)

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = ext.Get(ctx, "evil.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink escapes mount point")
}

func TestExternal_Get_SymlinkWithinMount_Allowed(t *testing.T) {
	tmpDir := t.TempDir()

	targetFile := filepath.Join(tmpDir, "target.txt")
	content := []byte("target content")
	err := os.WriteFile(targetFile, content, 0o644)
	require.NoError(t, err)

	linkFile := filepath.Join(tmpDir, "link.txt")
	err = os.Symlink(targetFile, linkFile)
	require.NoError(t, err)

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	retrieved, err := ext.Get(ctx, "link.txt")
	require.NoError(t, err)
	assert.Equal(t, content, retrieved)
}

func TestExternal_Get_DotDotFilename_Allowed(t *testing.T) {
	tmpDir := t.TempDir()

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

	dotDotDir := filepath.Join(tmpDir, "..dir")
	err := os.MkdirAll(dotDotDir, 0o755)
	require.NoError(t, err)

	dotDotDirFile := filepath.Join(dotDotDir, "test.txt")
	dotDotDirContent := []byte("file in ..dir")
	err = os.WriteFile(dotDotDirFile, dotDotDirContent, 0o644)
	require.NoError(t, err)

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	for _, tc := range testCases {
		retrieved, err := ext.Get(ctx, tc.name)
		require.NoError(t, err, "should allow file: %s", tc.name)
		assert.Equal(t, tc.content, retrieved)
	}

	retrieved, err := ext.Get(ctx, "..dir/test.txt")
	require.NoError(t, err, "should allow file in ..dir/")
	assert.Equal(t, dotDotDirContent, retrieved)
}
