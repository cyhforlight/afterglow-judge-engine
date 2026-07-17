package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspace_CreateAndCleanup(t *testing.T) {
	ws, err := newWorkspace()
	require.NoError(t, err)
	require.NotNil(t, ws)

	dir := ws.dir()
	assert.NotEmpty(t, dir)

	_, err = os.Stat(dir)
	require.NoError(t, err)

	err = ws.cleanup()
	require.NoError(t, err)

	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err))
}

func TestWorkspace_RejectsUnsafeNames(t *testing.T) {
	ws, err := newWorkspace()
	require.NoError(t, err)
	defer func() { _ = ws.cleanup() }()

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
			err := ws.writeFiles([]File{{Name: tt.path, Content: []byte("content"), Mode: 0o644}})
			require.Error(t, err)
		})
	}
}

func TestWorkspace_SymlinkEscapeRejected(t *testing.T) {
	ws, err := newWorkspace()
	require.NoError(t, err)
	defer func() { _ = ws.cleanup() }()

	outsideFile := filepath.Join(t.TempDir(), "outside.txt")
	err = os.WriteFile(outsideFile, []byte("outside"), 0o644)
	require.NoError(t, err)
	err = os.Symlink(outsideFile, filepath.Join(ws.dir(), "escape.txt"))
	require.NoError(t, err)

	_, err = ws.readFile("escape.txt")
	require.Error(t, err)
}
