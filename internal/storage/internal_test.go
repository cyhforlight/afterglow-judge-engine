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
	err := os.WriteFile(filepath.Join(tmpDir, "test-resource.txt"), []byte("test resource content"), 0o644)
	require.NoError(t, err)

	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)
	ctx := context.Background()

	data, err := storage.Get(ctx, "test-resource.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("test resource content"), data)
}

func TestInternalStorage_Get_NestedPath(t *testing.T) {
	tmpDir := t.TempDir()
	checkersDir := filepath.Join(tmpDir, "checkers")
	err := os.MkdirAll(checkersDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(checkersDir, "ncmp.cpp"), []byte("int main() {}"), 0o644)
	require.NoError(t, err)

	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	data, err := storage.Get(context.Background(), "checkers/ncmp.cpp")
	require.NoError(t, err)
	assert.Equal(t, []byte("int main() {}"), data)
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

func TestInternalStorage_Get_RejectsInvalidKeys(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("ok"), 0o644)
	require.NoError(t, err)

	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	tests := []struct {
		name  string
		key   string
		error string
	}{
		{name: "empty", key: "", error: "resource key is required"},
		{name: "absolute", key: "/etc/passwd", error: "resource key must be relative"},
		{name: "parent traversal", key: "../secret.txt", error: "escapes base directory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := storage.Get(context.Background(), tt.key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.error)
		})
	}
}

func TestInternalStorage_Get_ReturnsCopy(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello"), 0o644)
	require.NoError(t, err)

	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	firstRead, err := storage.Get(context.Background(), "test.txt")
	require.NoError(t, err)
	firstRead[0] = 'H'

	secondRead, err := storage.Get(context.Background(), "test.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), secondRead)
}

func TestInternalStorage_SnapshotSurvivesSourceRemoval(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "checkers")
	err := os.MkdirAll(nestedDir, 0o755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir, "testlib.h"), []byte("header"), 0o644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(nestedDir, "ncmp.cpp"), []byte("checker"), 0o644)
	require.NoError(t, err)

	storage, err := NewInternalStorage(tmpDir)
	require.NoError(t, err)

	err = os.RemoveAll(tmpDir)
	require.NoError(t, err)

	header, err := storage.Get(context.Background(), "testlib.h")
	require.NoError(t, err)
	assert.Equal(t, []byte("header"), header)

	checker, err := storage.Get(context.Background(), "checkers/ncmp.cpp")
	require.NoError(t, err)
	assert.Equal(t, []byte("checker"), checker)
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

func TestSupportDirFromExecutable(t *testing.T) {
	projectDir := t.TempDir()
	binDir := filepath.Join(projectDir, "bin")
	err := os.MkdirAll(filepath.Join(projectDir, bundledSupportDirName), 0o755)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(binDir, 0o755))

	executablePath := filepath.Join(projectDir, "judge-server")
	require.NoError(t, os.WriteFile(executablePath, []byte("#!/bin/sh\n"), 0o755))

	supportDir, err := supportDirFromExecutable(executablePath)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(projectDir, bundledSupportDirName), supportDir)
}

func TestSupportDirFromExecutable_ResolvesSymlink(t *testing.T) {
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, bundledSupportDirName), 0o755))

	realExecutable := filepath.Join(projectDir, "judge-server")
	require.NoError(t, os.WriteFile(realExecutable, []byte("#!/bin/sh\n"), 0o755))

	linkDir := t.TempDir()
	linkPath := filepath.Join(linkDir, "judge-server")
	require.NoError(t, os.Symlink(realExecutable, linkPath))

	supportDir, err := supportDirFromExecutable(linkPath)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(projectDir, bundledSupportDirName), supportDir)
}
