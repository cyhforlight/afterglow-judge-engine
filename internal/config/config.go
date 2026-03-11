// Package config provides configuration management for the sandbox server.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all server configuration.
type Config struct {
	// HTTP Server
	HTTPAddr        string
	HTTPPort        int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	ShutdownTimeout time.Duration

	// Containerd
	ContainerdSocket    string
	ContainerdNamespace string

	// Execution Limits
	MaxInputSizeMB int
	DefaultChecker string

	// Security
	APIKey string

	// Observability
	LogLevel string
}

// Load creates a Config from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		// HTTP Server
		HTTPAddr:        getEnv("HTTP_ADDR", "0.0.0.0"),
		HTTPPort:        getEnvInt("HTTP_PORT", 8080),
		ReadTimeout:     getEnvDuration("HTTP_READ_TIMEOUT", 30*time.Second),
		WriteTimeout:    getEnvDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		ShutdownTimeout: getEnvDuration("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),

		// Containerd
		ContainerdSocket:    getEnv("CONTAINERD_SOCKET", "/run/containerd/containerd.sock"),
		ContainerdNamespace: getEnv("CONTAINERD_NAMESPACE", "afterglow-sandbox"),

		// Execution Limits
		MaxInputSizeMB: getEnvInt("MAX_INPUT_SIZE_MB", 256),
		DefaultChecker: getEnv("DEFAULT_CHECKER", "default"),

		// Security
		APIKey: os.Getenv("API_KEY"),

		// Observability
		LogLevel: getEnv("LOG_LEVEL", "info"),
	}
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt retrieves an integer environment variable or returns a default value.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// getEnvDuration retrieves a duration environment variable or returns a default value.
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
