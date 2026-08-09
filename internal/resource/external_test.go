package resource

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExternal_OpensOnlyRegularFilesWithinRoot(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.Mkdir(filepath.Join(tmpDir, "cases"), 0o755)
	require.NoError(t, err)
	err = os.Symlink("/etc/passwd", filepath.Join(tmpDir, "evil.txt"))
	require.NoError(t, err)

	ext, err := NewExternal(tmpDir)
	require.NoError(t, err)

	tests := []struct {
		name string
		path string
	}{
		{name: "directory", path: "cases"},
		{name: "path traversal", path: "../../../etc/passwd"},
		{name: "symlink escape", path: "evil.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := ext.Open(tt.path)
			require.Error(t, err)
			require.Nil(t, file)
		})
	}
}
