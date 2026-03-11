package config

import (
	"os"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/service"

	"github.com/stretchr/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear environment
	clearEnv()

	cfg := Load()

	assert.Equal(t, "0.0.0.0", cfg.HTTPAddr)
	assert.Equal(t, 8080, cfg.HTTPPort)
	assert.Equal(t, 30*time.Second, cfg.ReadTimeout)
	assert.Equal(t, "/run/containerd/containerd.sock", cfg.ContainerdSocket)
	assert.Equal(t, "default", cfg.DefaultChecker)
	assert.Equal(t, service.BuiltinCheckerNames(), cfg.AllowedCheckers)
	assert.False(t, cfg.EnableAuth)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_FromEnv(t *testing.T) {
	clearEnv()

	_ = os.Setenv("HTTP_ADDR", "127.0.0.1")
	_ = os.Setenv("HTTP_PORT", "9000")
	_ = os.Setenv("HTTP_READ_TIMEOUT", "1m")
	_ = os.Setenv("DEFAULT_CHECKER", "ncmp")
	_ = os.Setenv("ALLOWED_CHECKERS", "default,ncmp,wcmp")
	_ = os.Setenv("ENABLE_AUTH", "true")
	_ = os.Setenv("API_KEYS", "key1,key2,key3")
	_ = os.Setenv("LOG_LEVEL", "debug")

	defer clearEnv()

	cfg := Load()

	assert.Equal(t, "127.0.0.1", cfg.HTTPAddr)
	assert.Equal(t, 9000, cfg.HTTPPort)
	assert.Equal(t, time.Minute, cfg.ReadTimeout)
	assert.Equal(t, "ncmp", cfg.DefaultChecker)
	assert.Equal(t, []string{"default", "ncmp", "wcmp"}, cfg.AllowedCheckers)
	assert.True(t, cfg.EnableAuth)
	assert.Equal(t, []string{"key1", "key2", "key3"}, cfg.APIKeys)
	assert.Equal(t, "debug", cfg.LogLevel)
}

func TestConfig_Addr(t *testing.T) {
	cfg := &Config{
		HTTPAddr: "localhost",
		HTTPPort: 8080,
	}

	assert.Equal(t, "localhost:8080", cfg.Addr())
}

func clearEnv() {
	envVars := []string{
		"HTTP_ADDR", "HTTP_PORT", "HTTP_READ_TIMEOUT",
		"HTTP_WRITE_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT",
		"CONTAINERD_SOCKET", "CONTAINERD_NAMESPACE",
		"MAX_INPUT_SIZE_MB",
		"DEFAULT_CHECKER", "ALLOWED_CHECKERS",
		"ENABLE_AUTH", "API_KEYS",
		"ALLOWED_ORIGINS", "LOG_LEVEL",
	}
	for _, v := range envVars {
		_ = os.Unsetenv(v)
	}
}
