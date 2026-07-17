// Package config provides configuration management for the sandbox server.
package config

import (
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"afterglow-judge-engine/internal/model"
)

// Config holds all server configuration.
type Config struct {
	// HTTP Server
	HTTPAddr         string
	HTTPPort         int
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration

	// Containerd
	ContainerdSocket    string
	ContainerdNamespace string

	// Execution Limits
	MaxInputBytes           int64
	MaxConcurrentContainers int
	MaxConcurrentJudges     int
	JudgeLimits             model.JudgeLimits
	ExternalDataDir         string

	// Observability
	LogLevel slog.Level
}

// Load reads environment variables, applies defaults, and converts their representation.
func Load() (*Config, error) {
	cfg := &Config{
		// HTTP Server
		HTTPAddr: getEnv("HTTP_ADDR", "0.0.0.0"),

		// Containerd
		ContainerdSocket:    getEnv("CONTAINERD_SOCKET", "/run/containerd/containerd.sock"),
		ContainerdNamespace: getEnv("CONTAINERD_NAMESPACE", "afterglow-sandbox"),

		// Execution Limits
		ExternalDataDir: getEnv("EXTERNAL_DATA_DIR", ""),
	}

	httpPort, err := getEnvInt("HTTP_PORT", 8080)
	if err != nil {
		return nil, err
	}
	cfg.HTTPPort = httpPort

	httpReadTimeout, err := getEnvDuration("HTTP_READ_TIMEOUT_MS", 30*time.Second)
	if err != nil {
		return nil, err
	}
	cfg.HTTPReadTimeout = httpReadTimeout

	httpWriteTimeout, err := getEnvDuration("HTTP_WRITE_TIMEOUT_MS", 10*time.Minute)
	if err != nil {
		return nil, err
	}
	cfg.HTTPWriteTimeout = httpWriteTimeout

	maxInputBytes, err := getEnvBytes("MAX_INPUT_SIZE_MB", 256)
	if err != nil {
		return nil, err
	}
	cfg.MaxInputBytes = maxInputBytes

	maxContainers, err := getEnvInt("MAX_CONCURRENT_CONTAINERS", 8)
	if err != nil {
		return nil, err
	}
	cfg.MaxConcurrentContainers = maxContainers

	maxJudges, err := getEnvInt("MAX_CONCURRENT_JUDGES", 4)
	if err != nil {
		return nil, err
	}
	cfg.MaxConcurrentJudges = maxJudges

	judgeLimits := model.DefaultJudgeLimits()
	judgeLimits.MaxTimeLimitMs, err = getEnvInt("MAX_TIME_LIMIT_MS", judgeLimits.MaxTimeLimitMs)
	if err != nil {
		return nil, err
	}
	judgeLimits.MaxMemoryMB, err = getEnvInt("MAX_MEMORY_MB", judgeLimits.MaxMemoryMB)
	if err != nil {
		return nil, err
	}
	judgeLimits.MaxTestCases, err = getEnvInt("MAX_TEST_CASES", judgeLimits.MaxTestCases)
	if err != nil {
		return nil, err
	}
	maxSourceSizeKB, err := getEnvInt("MAX_SOURCE_SIZE_KB", judgeLimits.MaxSourceBytes/1024)
	if err != nil {
		return nil, err
	}
	if maxSourceSizeKB > math.MaxInt/1024 || maxSourceSizeKB < math.MinInt/1024 {
		return nil, fmt.Errorf("MAX_SOURCE_SIZE_KB cannot be represented in bytes, got %d", maxSourceSizeKB)
	}
	judgeLimits.MaxSourceBytes = maxSourceSizeKB * 1024
	cfg.JudgeLimits = judgeLimits

	logLevel := getEnv("LOG_LEVEL", "info")
	if err := cfg.LogLevel.UnmarshalText([]byte(logLevel)); err != nil {
		return nil, fmt.Errorf("LOG_LEVEL must be a valid slog level, got %q: %w", logLevel, err)
	}

	return cfg, nil
}

// getEnv retrieves a string environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return defaultValue
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

func getEnvDuration(key string, defaultValue time.Duration) (time.Duration, error) {
	milliseconds, err := getEnvInt(key, int(defaultValue/time.Millisecond))
	if err != nil {
		return 0, err
	}
	if int64(milliseconds) > math.MaxInt64/int64(time.Millisecond) ||
		int64(milliseconds) < math.MinInt64/int64(time.Millisecond) {
		return 0, fmt.Errorf("%s cannot be represented as a duration, got %dms", key, milliseconds)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func getEnvBytes(key string, defaultMiB int) (int64, error) {
	const bytesPerMiB = int64(1024 * 1024)

	mebibytes, err := getEnvInt(key, defaultMiB)
	if err != nil {
		return 0, err
	}
	if int64(mebibytes) > math.MaxInt64/bytesPerMiB || int64(mebibytes) < math.MinInt64/bytesPerMiB {
		return 0, fmt.Errorf("%s cannot be represented in bytes, got %dMB", key, mebibytes)
	}
	return int64(mebibytes) * bytesPerMiB, nil
}
