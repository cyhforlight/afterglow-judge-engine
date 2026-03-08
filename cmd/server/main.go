// Package main provides the HTTP server entry point for afterglow-judge-sandbox.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"afterglow-judge-sandbox/internal/config"
	"afterglow-judge-sandbox/internal/sandbox"
	"afterglow-judge-sandbox/internal/service"
	"afterglow-judge-sandbox/internal/storage"
	"afterglow-judge-sandbox/internal/transport/httptransport"
)

func main() {
	cfg := config.Load()
	logger := setupLogger(cfg.LogLevel)

	logger.Info("starting sandbox server", "addr", cfg.Addr())

	judgeService, err := initializeComponents(cfg)
	if err != nil {
		logger.Error("initialization failed", "error", err)
		os.Exit(1)
	}

	server := httptransport.NewServer(cfg, judgeService, logger)

	if err := runServer(server, cfg, logger); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}

	logger.Info("server stopped gracefully")
}

func setupLogger(logLevel string) *slog.Logger {
	level := slog.LevelInfo
	if logLevel == "debug" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func initializeComponents(cfg *config.Config) (service.JudgeService, error) {
	// 1. Create shared Sandbox instance
	sb := sandbox.NewContainerdSandbox(cfg.ContainerdSocket, cfg.ContainerdNamespace)

	// 2. Load bundled internal resources before the service starts listening.
	if _, err := storage.NewBundledInternalStorage(); err != nil {
		return nil, fmt.Errorf("initialize internal storage: %w", err)
	}

	// 3. Create CacheStorage instance (not a global singleton)
	cacheDir := filepath.Join(os.TempDir(), "afterglow-compile-cache")
	cacheStorage, err := storage.NewCacheStorage(cacheDir, 500)
	if err != nil {
		slog.Warn("failed to initialize cache storage", "error", err)
		cacheStorage = nil // Allow running without cache
	}

	// 4. Inject dependencies into Runner and Compiler
	runner := service.NewUserCodeRunner(service.NewRunner(sb))
	compiler := service.NewUserCodeCompiler(service.NewCompiler(sb, cacheStorage))
	judge := service.NewJudgeEngine(runner, compiler)

	ctx := context.Background()
	if err := judge.PreflightCheck(ctx); err != nil {
		return nil, fmt.Errorf("preflight check failed: %w", err)
	}

	return judge, nil
}

func runServer(server *httptransport.Server, cfg *config.Config, logger *slog.Logger) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	errChan := make(chan error, 1)
	go func() {
		if err := server.Start(serverCtx); err != nil {
			errChan <- err
		}
	}()

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
		serverCancel()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		return server.Stop(shutdownCtx)

	case err := <-errChan:
		return err
	}
}
