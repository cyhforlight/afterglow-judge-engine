package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveChecker_Default(t *testing.T) {
	loc, err := ResolveChecker("")
	require.NoError(t, err)
	assert.Equal(t, "default", loc.Path)
	assert.False(t, loc.IsExternal)
}

func TestResolveChecker_Builtin(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPath string
		wantErr  string
	}{
		{name: "valid name", input: "ncmp", wantPath: "ncmp"},
		{name: "another valid", input: "yesno", wantPath: "yesno"},
		{name: "uppercase allowed", input: "NCMP", wantPath: "NCMP"},
		{name: "mixed case allowed", input: "MyChecker", wantPath: "MyChecker"},
		{name: "underscore allowed", input: "my_checker", wantPath: "my_checker"},
		{name: "hyphen allowed", input: "ncmp-v2", wantPath: "ncmp-v2"},
		{name: "file extension rejected", input: "ncmp.cpp", wantErr: `invalid path characters`},
		{name: "path rejected", input: "../ncmp", wantErr: `invalid path characters`},
		{name: "special char rejected", input: "ncmp@v2", wantErr: `invalid characters`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveChecker(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, got.Path)
			assert.False(t, got.IsExternal)
		})
	}
}

func TestResolveChecker_External(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPath string
		wantErr  string
	}{
		{
			name:     "valid path",
			input:    "external:testcase-15/checker.cpp",
			wantPath: "testcase-15/checker.cpp",
		},
		{
			name:     "normalized path",
			input:    "external:a/../b/checker.cpp",
			wantPath: "b/checker.cpp",
		},
		{name: "path traversal rejected", input: "external:../etc/passwd", wantErr: "resource key escapes base directory"},
		{name: "non-cpp rejected", input: "external:script.sh", wantErr: "must be a .cpp file"},
		{name: "empty path", input: "external:", wantErr: "resource key is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveChecker(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, got.Path)
			assert.True(t, got.IsExternal)
		})
	}
}
