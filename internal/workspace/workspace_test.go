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

func TestWorkspace_RejectsUnsafeNames(t *testing.T) {
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ws.WriteFile(tt.path, []byte("content"), 0o644)
			require.Error(t, err)
		})
	}
}

func TestWorkspace_SymlinkEscapeRejected(t *testing.T) {
	ws, err := New()
	require.NoError(t, err)
	defer func() { _ = ws.Cleanup() }()

	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	err = os.WriteFile(outsideFile, []byte("outside"), 0o644)
	require.NoError(t, err)
	err = os.Symlink(outsideFile, filepath.Join(ws.Dir(), "escape.txt"))
	require.NoError(t, err)

	_, err = ws.ReadFile("escape.txt")
	require.Error(t, err)
}
