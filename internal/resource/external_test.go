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

func TestExternal_Get_PathTraversal(t *testing.T) {
	tmpDir := t.TempDir()

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	ctx := context.Background()

	_, err = ext.Get(ctx, "../../../etc/passwd")
	require.Error(t, err)
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
}
