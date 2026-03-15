package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear environment
	clearEnv()

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "0.0.0.0", cfg.HTTPAddr)
	assert.Equal(t, 8080, cfg.HTTPPort)
	assert.Equal(t, "/run/containerd/containerd.sock", cfg.ContainerdSocket)
	assert.Empty(t, cfg.ExternalDataDir)
	assert.Empty(t, cfg.APIKey)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_FromEnv(t *testing.T) {
	clearEnv()

	tmpDir := t.TempDir()

	t.Setenv("HTTP_ADDR", "127.0.0.1")
	t.Setenv("HTTP_PORT", "9000")
	t.Setenv("EXTERNAL_DATA_DIR", tmpDir)
	t.Setenv("API_KEY", "my-secret-key")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.HTTPAddr)
	assert.Equal(t, 9000, cfg.HTTPPort)
	assert.Equal(t, tmpDir, cfg.ExternalDataDir)
	assert.Equal(t, "my-secret-key", cfg.APIKey)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestLoad_ExternalDataDirDisabledWhenUnsetOrBlank(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{
			name:  "unset",
			value: "",
		},
		{
			name:  "blank",
			value: "   ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv()
			if tt.name == "blank" {
				t.Setenv("EXTERNAL_DATA_DIR", tt.value)
			}

			cfg, err := Load()

			require.NoError(t, err)
			assert.Empty(t, cfg.ExternalDataDir)
		})
	}
}

func TestLoad_InvalidConfig(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		value       string
		wantMessage string
	}{
		{
			name:        "invalid integer is rejected",
			key:         "HTTP_PORT",
			value:       "abc",
			wantMessage: `HTTP_PORT must be an integer`,
		},
		{
			name:        "non-positive input size is rejected",
			key:         "MAX_INPUT_SIZE_MB",
			value:       "0",
			wantMessage: `MAX_INPUT_SIZE_MB must be positive`,
		},
		{
			name:        "non-positive concurrency is rejected",
			key:         "MAX_CONCURRENT_CONTAINERS",
			value:       "-1",
			wantMessage: `MAX_CONCURRENT_CONTAINERS must be positive`,
		},
		{
			name:        "unknown log level is rejected",
			key:         "LOG_LEVEL",
			value:       "trace",
			wantMessage: `LOG_LEVEL must be one of [info debug]`,
		},
		{
			name:        "relative external data dir is rejected",
			key:         "EXTERNAL_DATA_DIR",
			value:       filepath.Join("relative", "testdata"),
			wantMessage: `EXTERNAL_DATA_DIR must be an absolute path`,
		},
		{
			name:        "missing external data dir is rejected",
			key:         "EXTERNAL_DATA_DIR",
			value:       "/tmp/afterglow-config-test-missing-dir",
			wantMessage: `EXTERNAL_DATA_DIR is not accessible`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv()
			t.Setenv(tt.key, tt.value)

			cfg, err := Load()

			assert.Nil(t, cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMessage)
		})
	}
}

func TestConfig_Addr(t *testing.T) {
	cfg := &Config{
		HTTPAddr: "localhost",
		HTTPPort: 8080,
	}

	addr := fmt.Sprintf("%s:%d", cfg.HTTPAddr, cfg.HTTPPort)
	assert.Equal(t, "localhost:8080", addr)
}

func clearEnv() {
	envVars := []string{
		"HTTP_ADDR", "HTTP_PORT",
		"CONTAINERD_SOCKET", "CONTAINERD_NAMESPACE",
		"MAX_INPUT_SIZE_MB", "MAX_CONCURRENT_CONTAINERS",
		"MAX_CONCURRENT_JUDGES",
		"EXTERNAL_DATA_DIR",
		"API_KEY", "LOG_LEVEL",
	}
	for _, v := range envVars {
		_ = os.Unsetenv(v)
	}
}
