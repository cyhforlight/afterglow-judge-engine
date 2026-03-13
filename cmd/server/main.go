// Package main provides the HTTP server entry point for afterglow-judge-engine.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"afterglow-judge-engine/internal/cache"
	"afterglow-judge-engine/internal/config"
	"afterglow-judge-engine/internal/resource"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/service"
	"afterglow-judge-engine/internal/transport/httptransport"
)

const httpShutdownTimeout = 10 * time.Second

func main() {
	cfg := config.Load()
	logger := setupLogger(cfg.LogLevel)

	logger.Info("starting sandbox server", "addr", fmt.Sprintf("%s:%d", cfg.HTTPAddr, cfg.HTTPPort))

	judgeService, err := initializeComponents(cfg)
	if err != nil {
		logger.Error("initialization failed", "error", err)
		os.Exit(1)
	}

	server := httptransport.NewServer(cfg, judgeService, logger)

	if err := runServer(server, logger); err != nil {
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
	bundledResources, err := resource.NewBundled()
	if err != nil {
		return nil, fmt.Errorf("initialize bundled resources: %w", err)
	}

	// 3. Create checker compile cache (not a global singleton).
	compileCache, err := cache.New(64)
	if err != nil {
		slog.Warn("failed to initialize cache", "error", err)
		compileCache = nil // Allow running without checker cache.
	}

	// 4. Create external resource store for test data files.
	var externalResources *resource.External
	if cfg.ExternalDataDir != "" {
		externalResources, err = resource.NewExternal(cfg.ExternalDataDir)
		if err != nil {
			slog.Warn("failed to initialize external resources", "error", err, "path", cfg.ExternalDataDir)
			externalResources = nil
		}
	}

	// 5. Create base compiler and runner primitives, throttled by a shared semaphore.
	containerSem := make(chan struct{}, cfg.MaxConcurrentContainers)
	compiler := service.NewThrottledCompiler(service.NewCompiler(sb), containerSem)
	runner := service.NewThrottledRunner(service.NewRunner(sb), containerSem)

	// 6. Create judge engine with internal checker resources.
	judge, err := service.NewJudgeEngine(
		compiler,
		runner,
		bundledResources,
		externalResources,
		cfg.DefaultChecker,
		compileCache,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize judge engine: %w", err)
	}

	ctx := context.Background()
	if err := judge.PreflightCheck(ctx); err != nil {
		return nil, fmt.Errorf("preflight check failed: %w", err)
	}

	return judge, nil
}
func runServer(server *httptransport.Server, logger *slog.Logger) error {
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

		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()

		return server.Stop(shutdownCtx)

	case err := <-errChan:
		return err
	}
}
