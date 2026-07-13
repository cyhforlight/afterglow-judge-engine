// Package main provides the HTTP server entry point for afterglow-judge-engine.
package main

import (
	"context"
	"fmt"
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
	// 1. Create shared Sandbox instance
	sb := sandbox.NewContainerdSandbox(cfg.ContainerdSocket, cfg.ContainerdNamespace)

	// 2. Load bundled internal resources before the service starts listening.
	bundledResources, err := resource.NewBundled()
	if err != nil {
		return nil, fmt.Errorf("initialize bundled resources: %w", err)
	}

	// 3. Optionally enable external test data / checker files when configured.
	var externalResources service.ResourceStore
	if cfg.ExternalDataDir != "" {
		ext, err := resource.NewExternal(cfg.ExternalDataDir)
		if err != nil {
			return nil, fmt.Errorf("initialize external resources %q: %w", cfg.ExternalDataDir, err)
		}
		externalResources = ext
	}

	// 4. Create shared execution primitives.
	containerSem := make(chan struct{}, cfg.MaxConcurrentContainers)
	executor := execution.NewThrottledExecutor(execution.NewExecutor(sb), containerSem)
	compiler := service.NewCompiler(executor)
	checkerCompiler, err := service.NewCachedCompiler(compiler, 64)
	if err != nil {
		return nil, fmt.Errorf("initialize checker compile cache: %w", err)
	}
	runner := service.NewRunner(executor)

	// 5. Create judge engine with internal checker resources.
	judge := service.NewJudgeEngine(
		compiler,
		checkerCompiler,
		runner,
		bundledResources,
		externalResources,
		cfg.MaxConcurrentJudges,
		cfg.JudgeLimits,
	)

	ctx := context.Background()
	if err := judge.PreflightCheck(ctx); err != nil {
		return nil, fmt.Errorf("preflight check failed: %w", err)
	}

	return judge, nil
}
