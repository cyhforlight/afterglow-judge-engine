package workspace

import (
	"os"
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

func TestWorkspace_WriteFile(t *testing.T) {
	ws, err := New()
	require.NoError(t, err)
	defer func() { _ = ws.Cleanup() }()

	content := []byte("test content")
	err = ws.WriteFile("test.txt", content, 0644)
	require.NoError(t, err)

	readContent, err := os.ReadFile(ws.Dir() + "/test.txt")
	require.NoError(t, err)
	assert.Equal(t, content, readContent)
}
