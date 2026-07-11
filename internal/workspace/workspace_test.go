package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspace_CreateAndCleanup(t *testing.T) {
	ws, err := New()
	require.NoError(t, err)
	require.NotNil(t, ws)

	dir := ws.Dir()
	assert.NotEmpty(t, dir)

	// Verify directory exists
	_, err = os.Stat(dir)
	require.NoError(t, err)

	// Cleanup
	err = ws.Cleanup()
	require.NoError(t, err)

	// Verify directory is removed
	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err))
}

func TestWorkspace_WriteFilesAndReadFile(t *testing.T) {
	ws, err := New()
	require.NoError(t, err)
	defer func() { _ = ws.Cleanup() }()

	err = ws.WriteFiles([]File{
		{Name: "main.cpp", Content: []byte("int main(){}"), Mode: 0o644},
		{Name: "program", Content: []byte("binary"), Mode: 0o755},
	})
	require.NoError(t, err)

	source, err := ws.ReadFile("main.cpp")
	require.NoError(t, err)
	assert.Equal(t, []byte("int main(){}"), source)

	info, err := ws.Stat("program")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestWorkspace_ResolvePathRejectsUnsafeNames(t *testing.T) {
	ws, err := New()
	require.NoError(t, err)
	defer func() { _ = ws.Cleanup() }()

	tests := []struct {
		name string
		path string
	}{
		{name: "empty", path: ""},
		{name: "current directory", path: "."},
		{name: "parent escape", path: "../escape.txt"},
		{name: "nested parent escape", path: "dir/../../escape.txt"},
		{name: "absolute", path: filepath.Join(string(filepath.Separator), "tmp", "escape.txt")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ws.resolvePath(tt.path)
			require.Error(t, err)
		})
	}
}

func TestWorkspace_ResolvePathAllowsSafeNestedName(t *testing.T) {
	ws, err := New()
	require.NoError(t, err)
	defer func() { _ = ws.Cleanup() }()

	path, err := ws.resolvePath("dir/file.txt")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ws.Dir(), "dir", "file.txt"), path)
}
