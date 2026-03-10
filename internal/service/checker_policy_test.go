package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCheckerPolicy_Defaults(t *testing.T) {
	policy, err := NewCheckerPolicy("", nil)
	require.NoError(t, err)

	resolved, err := policy.Resolve("")
	require.NoError(t, err)
	assert.Equal(t, defaultCheckerName, resolved.Path)
	assert.False(t, resolved.IsExternal)
}

func TestCheckerPolicy_Resolve(t *testing.T) {
	policy, err := NewCheckerPolicy(defaultCheckerName, []string{defaultCheckerName, "ncmp", "wcmp"})
	require.NoError(t, err)

	tests := []struct {
		name         string
		input        string
		wantPath     string
		wantExternal bool
		wantErr      string
	}{
		{name: "empty uses default", input: "", wantPath: defaultCheckerName, wantExternal: false},
		{name: "allowed checker", input: "ncmp", wantPath: "ncmp", wantExternal: false},
		{name: "disallowed checker", input: "yesno", wantErr: `checker "yesno" is not allowed`},
		{name: "file name rejected", input: "ncmp.cpp", wantErr: `checker "ncmp.cpp" must be a builtin short name`},
		{name: "path rejected", input: "../ncmp", wantErr: `checker "../ncmp" must be a builtin short name`},
		{name: "uppercase rejected", input: "NCMP", wantErr: `checker "NCMP" must be a builtin short name`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.Resolve(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, got.Path)
			assert.Equal(t, tt.wantExternal, got.IsExternal)
		})
	}
}

func TestNewCheckerPolicy_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name           string
		defaultChecker string
		allowed        []string
		wantErr        string
	}{
		{
			name:           "default not allowed",
			defaultChecker: "ncmp",
			allowed:        []string{defaultCheckerName},
			wantErr:        `default checker "ncmp" is not in allowed checkers`,
		},
		{
			name:           "invalid allowed checker",
			defaultChecker: defaultCheckerName,
			allowed:        []string{defaultCheckerName, "ncmp.cpp"},
			wantErr:        `allowed checker "ncmp.cpp"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCheckerPolicy(tt.defaultChecker, tt.allowed)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCheckerPolicy_ExternalCheckers(t *testing.T) {
	policy, err := NewCheckerPolicy(defaultCheckerName, []string{defaultCheckerName, "external:*"})
	require.NoError(t, err)

	tests := []struct {
		name         string
		input        string
		wantPath     string
		wantExternal bool
		wantErr      string
	}{
		{
			name:         "external checker with valid path",
			input:        "external:testcase-15/checker.cpp",
			wantPath:     "testcase-15/checker.cpp",
			wantExternal: true,
		},
		{
			name:         "external checker normalized path",
			input:        "external:a/../b/checker.cpp",
			wantPath:     "b/checker.cpp",
			wantExternal: true,
		},
		{
			name:    "external checker path traversal rejected",
			input:   "external:../etc/passwd",
			wantErr: "resource key escapes base directory",
		},
		{
			name:    "external checker non-cpp rejected",
			input:   "external:script.sh",
			wantErr: "must be a .cpp file",
		},
		{
			name:    "external checker empty path",
			input:   "external:",
			wantErr: "resource key is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := policy.Resolve(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, got.Path)
			assert.Equal(t, tt.wantExternal, got.IsExternal)
		})
	}
}

func TestCheckerPolicy_ExternalCheckerWildcardPatterns(t *testing.T) {
	tests := []struct {
		name        string
		allowed     []string
		input       string
		shouldAllow bool
	}{
		{
			name:        "wildcard allows all external",
			allowed:     []string{defaultCheckerName, "external:*"},
			input:       "external:any/path/checker.cpp",
			shouldAllow: true,
		},
		{
			name:        "prefix pattern allows matching",
			allowed:     []string{defaultCheckerName, "external:contest-2024/*"},
			input:       "external:contest-2024/problem-a/checker.cpp",
			shouldAllow: true,
		},
		{
			name:        "prefix pattern rejects non-matching",
			allowed:     []string{defaultCheckerName, "external:contest-2024/*"},
			input:       "external:contest-2025/checker.cpp",
			shouldAllow: false,
		},
		{
			name:        "exact match allowed",
			allowed:     []string{defaultCheckerName, "external:special/checker.cpp"},
			input:       "external:special/checker.cpp",
			shouldAllow: true,
		},
		{
			name:        "no external allowed",
			allowed:     []string{defaultCheckerName},
			input:       "external:any/checker.cpp",
			shouldAllow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := NewCheckerPolicy(defaultCheckerName, tt.allowed)
			require.NoError(t, err)

			got, err := policy.Resolve(tt.input)
			if tt.shouldAllow {
				require.NoError(t, err)
				assert.True(t, got.IsExternal)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "is not allowed")
			}
		})
	}
}
