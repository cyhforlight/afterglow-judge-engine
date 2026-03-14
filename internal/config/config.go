// Package config provides configuration management for the sandbox server.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds all server configuration.
type Config struct {
	// HTTP Server
	HTTPAddr string
	HTTPPort int

	// Containerd
	ContainerdSocket    string
	ContainerdNamespace string

	// Execution Limits
	MaxInputSizeMB          int
	MaxConcurrentContainers int
	DefaultChecker          string
	ExternalDataDir         string

	// Security
	APIKey string

	// Observability
	LogLevel string
}

// Load creates a Config from environment variables and validates it.
func Load() (*Config, error) {
	cfg := &Config{
		// HTTP Server
		HTTPAddr: getEnv("HTTP_ADDR", "0.0.0.0"),

		// Containerd
		ContainerdSocket:    getEnv("CONTAINERD_SOCKET", "/run/containerd/containerd.sock"),
		ContainerdNamespace: getEnv("CONTAINERD_NAMESPACE", "afterglow-sandbox"),

		// Execution Limits
		DefaultChecker:  getEnv("DEFAULT_CHECKER", "default"),
		ExternalDataDir: getEnv("EXTERNAL_DATA_DIR", "/home/forlight/afterglow-judge-engine/testdata"),

		// Security
		APIKey: getOptionalEnv("API_KEY"),

		// Observability
		LogLevel: getEnv("LOG_LEVEL", "info"),
	}

	httpPort, err := getEnvInt("HTTP_PORT", 8080)
	if err != nil {
		return nil, err
	}
	cfg.HTTPPort = httpPort

	maxInputSizeMB, err := getEnvInt("MAX_INPUT_SIZE_MB", 256)
	if err != nil {
		return nil, err
	}
	cfg.MaxInputSizeMB = maxInputSizeMB

	maxContainers, err := getEnvInt("MAX_CONCURRENT_CONTAINERS", 8)
	if err != nil {
		return nil, err
	}
	cfg.MaxConcurrentContainers = maxContainers

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that a loaded Config is internally consistent.
func (cfg *Config) Validate() error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}

	if cfg.HTTPAddr == "" {
		return fmt.Errorf("HTTP_ADDR must not be empty")
	}
	if cfg.HTTPPort <= 0 || cfg.HTTPPort > 65535 {
		return fmt.Errorf("HTTP_PORT must be between 1 and 65535, got %d", cfg.HTTPPort)
	}
	if cfg.ContainerdSocket == "" {
		return fmt.Errorf("CONTAINERD_SOCKET must not be empty")
	}
	if cfg.ContainerdNamespace == "" {
		return fmt.Errorf("CONTAINERD_NAMESPACE must not be empty")
	}
	if cfg.MaxInputSizeMB <= 0 {
		return fmt.Errorf("MAX_INPUT_SIZE_MB must be positive, got %d", cfg.MaxInputSizeMB)
	}
	if cfg.MaxConcurrentContainers <= 0 {
		return fmt.Errorf("MAX_CONCURRENT_CONTAINERS must be positive, got %d", cfg.MaxConcurrentContainers)
	}
	if cfg.DefaultChecker == "" {
		return fmt.Errorf("DEFAULT_CHECKER must not be empty")
	}
	if cfg.ExternalDataDir == "" {
		return fmt.Errorf("EXTERNAL_DATA_DIR must not be empty")
	}
	if err := validateDirectory("EXTERNAL_DATA_DIR", cfg.ExternalDataDir); err != nil {
		return err
	}
	if err := validateLogLevel(cfg.LogLevel); err != nil {
		return err
	}

	return nil
}

// getEnv retrieves a string environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
	}
	return strings.TrimSpace(value)
}

func getOptionalEnv(key string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

// getEnvInt retrieves an integer environment variable or returns a default value.
func getEnvInt(key string, defaultValue int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("%s must not be empty", key)
	}

	intVal, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, value)
	}
	return intVal, nil
}

func validateDirectory(key, dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%s must be an absolute path, got %q", key, dir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%s is not accessible: %w", key, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must point to a directory, got %q", key, dir)
	}
	return nil
}

func validateLogLevel(level string) error {
	switch level {
	case "info", "debug":
		return nil
	default:
		return fmt.Errorf("LOG_LEVEL must be one of [info debug], got %q", level)
	}
}
