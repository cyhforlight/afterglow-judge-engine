package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-engine/internal/model"

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
	assert.Equal(t, int((30 * time.Second).Milliseconds()), cfg.HTTPReadTimeoutMs)
	assert.Equal(t, int((10 * time.Minute).Milliseconds()), cfg.HTTPWriteTimeoutMs)
	assert.Equal(t, "/run/containerd/containerd.sock", cfg.ContainerdSocket)
	assert.Equal(t, model.DefaultJudgeLimits(), cfg.JudgeLimits)
	assert.Empty(t, cfg.ExternalDataDir)
	assert.Empty(t, cfg.APIKey)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_FromEnv(t *testing.T) {
	clearEnv()

	tmpDir := t.TempDir()

	t.Setenv("HTTP_ADDR", "127.0.0.1")
	t.Setenv("HTTP_PORT", "9000")
	t.Setenv("HTTP_READ_TIMEOUT_MS", "10000")
	t.Setenv("HTTP_WRITE_TIMEOUT_MS", "180000")
	t.Setenv("MAX_TIME_LIMIT_MS", "20000")
	t.Setenv("MAX_MEMORY_MB", "2048")
	t.Setenv("MAX_TEST_CASES", "128")
	t.Setenv("MAX_SOURCE_SIZE_KB", "512")
	t.Setenv("EXTERNAL_DATA_DIR", tmpDir)
	t.Setenv("API_KEY", "my-secret-key")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.HTTPAddr)
	assert.Equal(t, 9000, cfg.HTTPPort)
	assert.Equal(t, 10000, cfg.HTTPReadTimeoutMs)
	assert.Equal(t, 180000, cfg.HTTPWriteTimeoutMs)
	assert.Equal(t, model.JudgeLimits{
		MaxTimeLimitMs: 20000,
		MaxMemoryMB:    2048,
		MaxTestCases:   128,
		MaxSourceBytes: 512 * 1024,
	}, cfg.JudgeLimits)
	assert.Equal(t, tmpDir, cfg.ExternalDataDir)
	assert.Equal(t, "my-secret-key", cfg.APIKey)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestLoad_ExternalDataDirDisabledWhenBlank(t *testing.T) {
	clearEnv()
	t.Setenv("EXTERNAL_DATA_DIR", "   ")

	cfg, err := Load()

	require.NoError(t, err)
	assert.Empty(t, cfg.ExternalDataDir)
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
			name:        "non-positive read timeout is rejected",
			key:         "HTTP_READ_TIMEOUT_MS",
			value:       "0",
			wantMessage: `HTTP_READ_TIMEOUT_MS must be positive`,
		},
		{
			name:        "non-positive write timeout is rejected",
			key:         "HTTP_WRITE_TIMEOUT_MS",
			value:       "-1",
			wantMessage: `HTTP_WRITE_TIMEOUT_MS must be positive`,
		},
		{
			name:        "non-positive concurrency is rejected",
			key:         "MAX_CONCURRENT_CONTAINERS",
			value:       "-1",
			wantMessage: `MAX_CONCURRENT_CONTAINERS must be positive`,
		},
		{
			name:        "non-positive time limit ceiling is rejected",
			key:         "MAX_TIME_LIMIT_MS",
			value:       "0",
			wantMessage: `MAX_TIME_LIMIT_MS must be positive`,
		},
		{
			name:        "non-positive memory ceiling is rejected",
			key:         "MAX_MEMORY_MB",
			value:       "0",
			wantMessage: `MAX_MEMORY_MB must be positive`,
		},
		{
			name:        "non-positive testcase ceiling is rejected",
			key:         "MAX_TEST_CASES",
			value:       "0",
			wantMessage: `MAX_TEST_CASES must be positive`,
		},
		{
			name:        "non-positive source size ceiling is rejected",
			key:         "MAX_SOURCE_SIZE_KB",
			value:       "0",
			wantMessage: `MAX_SOURCE_SIZE_KB must be positive`,
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

func clearEnv() {
	envVars := []string{
		"HTTP_ADDR", "HTTP_PORT",
		"HTTP_READ_TIMEOUT_MS", "HTTP_WRITE_TIMEOUT_MS",
		"CONTAINERD_SOCKET", "CONTAINERD_NAMESPACE",
		"MAX_INPUT_SIZE_MB", "MAX_CONCURRENT_CONTAINERS",
		"MAX_CONCURRENT_JUDGES",
		"MAX_TIME_LIMIT_MS", "MAX_MEMORY_MB",
		"MAX_TEST_CASES", "MAX_SOURCE_SIZE_KB",
		"EXTERNAL_DATA_DIR",
		"API_KEY", "LOG_LEVEL",
	}
	for _, v := range envVars {
		_ = os.Unsetenv(v)
	}
}
