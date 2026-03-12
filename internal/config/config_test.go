package config

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear environment
	clearEnv()

	cfg := Load()

	assert.Equal(t, "0.0.0.0", cfg.HTTPAddr)
	assert.Equal(t, 8080, cfg.HTTPPort)
	assert.Equal(t, "/run/containerd/containerd.sock", cfg.ContainerdSocket)
	assert.Equal(t, "default", cfg.DefaultChecker)
	assert.Empty(t, cfg.APIKey)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_FromEnv(t *testing.T) {
	clearEnv()

	_ = os.Setenv("HTTP_ADDR", "127.0.0.1")
	_ = os.Setenv("HTTP_PORT", "9000")
	_ = os.Setenv("DEFAULT_CHECKER", "ncmp")
	_ = os.Setenv("API_KEY", "my-secret-key")
	_ = os.Setenv("LOG_LEVEL", "debug")

	defer clearEnv()

	cfg := Load()

	assert.Equal(t, "127.0.0.1", cfg.HTTPAddr)
	assert.Equal(t, 9000, cfg.HTTPPort)
	assert.Equal(t, "ncmp", cfg.DefaultChecker)
	assert.Equal(t, "my-secret-key", cfg.APIKey)
	assert.Equal(t, "debug", cfg.LogLevel)
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
		"MAX_INPUT_SIZE_MB",
		"DEFAULT_CHECKER",
		"API_KEY", "LOG_LEVEL",
	}
	for _, v := range envVars {
		_ = os.Unsetenv(v)
	}
}
