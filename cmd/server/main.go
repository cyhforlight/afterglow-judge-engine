// Package main provides the HTTP server entry point for afterglow-judge-engine.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"afterglow-judge-engine/internal/config"
	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/resource"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/service"
	"afterglow-judge-engine/internal/transport/httptransport"

	"golang.org/x/sync/semaphore"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logger := setupLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	logger.Info("starting sandbox server", "addr", fmt.Sprintf("%s:%d", cfg.HTTPAddr, cfg.HTTPPort))

	judgeService, err := initializeComponents(cfg)
	if err != nil {
		logger.Error("initialization failed", "error", err)
		os.Exit(1)
	}

	server := httptransport.NewServer(cfg, judgeService, logger)

	serverCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	serverErr := server.Run(serverCtx)
	stop()

	if serverErr != nil {
		logger.Error("server error", "error", serverErr)
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
	// 1. Create the shared containerd sandbox.
	sb, err := sandbox.New(cfg.ContainerdSocket, cfg.ContainerdNamespace)
	if err != nil {
		return nil, fmt.Errorf("initialize sandbox: %w", err)
	}

	// 2. Load bundled internal resources before the service starts listening.
	bundledFS, err := resource.NewBundled()
	if err != nil {
		return nil, fmt.Errorf("initialize bundled resources: %w", err)
	}

	// 3. Optionally enable external test data / checker files when configured.
	var externalFS fs.FS
	if cfg.ExternalDataDir != "" {
		ext, err := resource.NewExternal(cfg.ExternalDataDir)
		if err != nil {
			return nil, fmt.Errorf("initialize external resources %q: %w", cfg.ExternalDataDir, err)
		}
		externalFS = ext
	}

	// 4. Create shared execution primitives.
	containerSem := semaphore.NewWeighted(int64(cfg.MaxConcurrentContainers))
	executor := execution.NewThrottledExecutor(execution.NewExecutor(sb), containerSem)
	compiler := service.NewCompiler(executor)
	runner := service.NewRunner(executor)

	// 5. Create judge engine with internal checker resources.
	judge, err := service.NewJudgeEngine(
		compiler,
		runner,
		bundledFS,
		externalFS,
		cfg.MaxConcurrentJudges,
		cfg.JudgeLimits,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize judge engine: %w", err)
	}

	ctx := context.Background()
	if err := sb.CheckEnvironment(ctx); err != nil {
		return nil, fmt.Errorf("sandbox environment check failed: %w", err)
	}

	return judge, nil
}
