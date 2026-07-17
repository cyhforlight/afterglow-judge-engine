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

	logger.Info("server stopped gracefully")
}

func setupLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func initializeServer(cfg *config.Config, logger *slog.Logger) (*httptransport.Server, error) {
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
	executor, err := execution.NewExecutor(sb, cfg.MaxConcurrentContainers)
	if err != nil {
		return nil, fmt.Errorf("initialize executor: %w", err)
	}

	// 5. Create judge engine with internal checker resources.
	judge, err := service.NewJudgeEngine(
		executor,
		bundledFS,
		externalFS,
		cfg.MaxConcurrentJudges,
		cfg.JudgeLimits,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize judge engine: %w", err)
	}

	// 6. Assemble the HTTP transport after its dependencies are ready.
	server, err := httptransport.NewServer(httptransport.ServerOptions{
		Addr:         cfg.HTTPAddr,
		Port:         cfg.HTTPPort,
		ReadTimeout:  cfg.HTTPReadTimeout,
		WriteTimeout: cfg.HTTPWriteTimeout,
		MaxBodyBytes: cfg.MaxInputBytes,
	}, judge, logger)
	if err != nil {
		return nil, fmt.Errorf("initialize HTTP server: %w", err)
	}

	// 7. Verify runtime dependencies only after all configuration is accepted.
	if err := sb.CheckEnvironment(context.Background()); err != nil {
		return nil, fmt.Errorf("sandbox environment check failed: %w", err)
	}

	return server, nil
}
