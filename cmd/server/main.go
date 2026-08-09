// Package main provides the HTTP server entry point for afterglow-judge-engine.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/resource"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/service"
	"afterglow-judge-engine/internal/transport/httptransport"
)

const containerdNamespace = "afterglow-sandbox"

type settings struct {
	listenAddr              string
	containerdSocket        string
	maxConcurrentContainers int
	maxConcurrentJudges     int
	externalDataDir         string
	logLevel                slog.Level
}

func main() {
	cfg, err := loadSettings()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load settings: %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg.logLevel)
	slog.SetDefault(logger)

	server, err := initializeServer(cfg, logger)
	if err != nil {
		logger.Error("initialization failed", "error", err)
		os.Exit(1)
	}

	serverCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	serverErr := server.Run(serverCtx)
	stop()

	if serverErr != nil {
		logger.Error("server error", "error", serverErr)
		os.Exit(1)
	}
}

func setupLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func loadSettings() (settings, error) {
	cfg := settings{
		listenAddr:       env("HTTP_LISTEN_ADDR", ":8080"),
		containerdSocket: env("CONTAINERD_SOCKET", "/run/containerd/containerd.sock"),
		externalDataDir:  env("EXTERNAL_DATA_DIR", ""),
	}
	if cfg.listenAddr == "" {
		return settings{}, errors.New("HTTP_LISTEN_ADDR must not be empty")
	}

	var err error
	cfg.maxConcurrentContainers, err = envInt("MAX_CONCURRENT_CONTAINERS", 8)
	if err != nil {
		return settings{}, err
	}
	cfg.maxConcurrentJudges, err = envInt("MAX_CONCURRENT_JUDGES", 4)
	if err != nil {
		return settings{}, err
	}

	logLevel := env("LOG_LEVEL", "info")
	if err := cfg.logLevel.UnmarshalText([]byte(logLevel)); err != nil {
		return settings{}, fmt.Errorf("LOG_LEVEL must be a valid slog level, got %q: %w", logLevel, err)
	}
	return cfg, nil
}

func env(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	return strings.TrimSpace(value)
}

func envInt(key string, fallback int) (int, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}

	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer, got %q", key, value)
	}
	return n, nil
}

func initializeServer(cfg settings, logger *slog.Logger) (*httptransport.Server, error) {
	sb, err := sandbox.New(cfg.containerdSocket, containerdNamespace)
	if err != nil {
		return nil, fmt.Errorf("initialize sandbox: %w", err)
	}

	bundledFS, err := resource.NewBundled()
	if err != nil {
		return nil, fmt.Errorf("initialize bundled resources: %w", err)
	}

	var externalFS fs.FS
	if cfg.externalDataDir != "" {
		ext, err := resource.NewExternal(cfg.externalDataDir)
		if err != nil {
			return nil, fmt.Errorf("initialize external resources %q: %w", cfg.externalDataDir, err)
		}
		externalFS = ext
	}

	executor, err := execution.NewExecutor(sb, cfg.maxConcurrentContainers)
	if err != nil {
		return nil, fmt.Errorf("initialize executor: %w", err)
	}

	judge, err := service.NewJudgeEngine(
		executor,
		bundledFS,
		externalFS,
		cfg.maxConcurrentJudges,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize judge engine: %w", err)
	}

	server := httptransport.NewServer(cfg.listenAddr, judge, logger)

	if err := sb.CheckEnvironment(context.Background()); err != nil {
		return nil, fmt.Errorf("sandbox environment check failed: %w", err)
	}

	return server, nil
}
