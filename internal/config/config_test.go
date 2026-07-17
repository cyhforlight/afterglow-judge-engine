package config

import (
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
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
	assert.Equal(t, 30*time.Second, cfg.HTTPReadTimeout)
	assert.Equal(t, 10*time.Minute, cfg.HTTPWriteTimeout)
	assert.Equal(t, "/run/containerd/containerd.sock", cfg.ContainerdSocket)
	assert.Equal(t, int64(256*1024*1024), cfg.MaxInputBytes)
	assert.Equal(t, model.DefaultJudgeLimits(), cfg.JudgeLimits)
	assert.Empty(t, cfg.ExternalDataDir)
	assert.Equal(t, slog.LevelInfo, cfg.LogLevel)
}

func TestLoad_FromEnv(t *testing.T) {
	clearEnv()

	tmpDir := t.TempDir()

	t.Setenv("HTTP_ADDR", "127.0.0.1")
	t.Setenv("HTTP_PORT", "9000")
	t.Setenv("HTTP_READ_TIMEOUT_MS", "10000")
	t.Setenv("HTTP_WRITE_TIMEOUT_MS", "180000")
	t.Setenv("MAX_INPUT_SIZE_MB", "128")
	t.Setenv("MAX_TIME_LIMIT_MS", "20000")
	t.Setenv("MAX_MEMORY_MB", "2048")
	t.Setenv("MAX_TEST_CASES", "128")
	t.Setenv("MAX_SOURCE_SIZE_KB", "512")
	t.Setenv("EXTERNAL_DATA_DIR", tmpDir)
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", cfg.HTTPAddr)
	assert.Equal(t, 9000, cfg.HTTPPort)
	assert.Equal(t, 10*time.Second, cfg.HTTPReadTimeout)
	assert.Equal(t, 3*time.Minute, cfg.HTTPWriteTimeout)
	assert.Equal(t, int64(128*1024*1024), cfg.MaxInputBytes)
	assert.Equal(t, model.JudgeLimits{
		MaxTimeLimitMs: 20000,
		MaxMemoryMB:    2048,
		MaxTestCases:   128,
		MaxSourceBytes: 512 * 1024,
	}, cfg.JudgeLimits)
	assert.Equal(t, tmpDir, cfg.ExternalDataDir)
	assert.Equal(t, slog.LevelDebug, cfg.LogLevel)
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
			name:        "unknown log level is rejected",
			key:         "LOG_LEVEL",
			value:       "trace",
			wantMessage: `LOG_LEVEL must be a valid slog level`,
		},
		{
			name:        "timeout conversion overflow is rejected",
			key:         "HTTP_READ_TIMEOUT_MS",
			value:       strconv.Itoa(math.MaxInt),
			wantMessage: `HTTP_READ_TIMEOUT_MS cannot be represented as a duration`,
		},
		{
			name:        "body size conversion overflow is rejected",
			key:         "MAX_INPUT_SIZE_MB",
			value:       strconv.Itoa(math.MaxInt),
			wantMessage: `MAX_INPUT_SIZE_MB cannot be represented in bytes`,
		},
		{
			name:        "source size conversion overflow is rejected",
			key:         "MAX_SOURCE_SIZE_KB",
			value:       strconv.Itoa(math.MaxInt),
			wantMessage: `MAX_SOURCE_SIZE_KB cannot be represented in bytes`,
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

func TestLoad_LeavesModuleSemanticsToConsumers(t *testing.T) {
	clearEnv()
	t.Setenv("HTTP_PORT", "0")
	t.Setenv("HTTP_READ_TIMEOUT_MS", "0")
	t.Setenv("MAX_INPUT_SIZE_MB", "-1")
	t.Setenv("MAX_CONCURRENT_CONTAINERS", "-1")
	t.Setenv("MAX_CONCURRENT_JUDGES", "0")
	t.Setenv("MAX_TIME_LIMIT_MS", "0")
	t.Setenv("EXTERNAL_DATA_DIR", filepath.Join("relative", "testdata"))

	cfg, err := Load()

	require.NoError(t, err)
	assert.Zero(t, cfg.HTTPPort)
	assert.Zero(t, cfg.HTTPReadTimeout)
	assert.Equal(t, int64(-1024*1024), cfg.MaxInputBytes)
	assert.Equal(t, -1, cfg.MaxConcurrentContainers)
	assert.Zero(t, cfg.MaxConcurrentJudges)
	assert.Zero(t, cfg.JudgeLimits.MaxTimeLimitMs)
	assert.Equal(t, filepath.Join("relative", "testdata"), cfg.ExternalDataDir)
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
		"EXTERNAL_DATA_DIR", "LOG_LEVEL",
	}
	for _, v := range envVars {
		_ = os.Unsetenv(v)
	}
}
