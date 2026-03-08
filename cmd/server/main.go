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

	"afterglow-judge-sandbox/internal/cache"
	"afterglow-judge-sandbox/internal/concurrency"
	"afterglow-judge-sandbox/internal/config"
	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
	"afterglow-judge-sandbox/internal/service"
	"afterglow-judge-sandbox/internal/transport/httptransport"
)

func main() {
	cfg := config.Load()
	logger := setupLogger(cfg.LogLevel)

	logger.Info("starting sandbox server",
		"addr", cfg.Addr(),
		"max_concurrent", cfg.MaxConcurrentExecutions,
	)

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
	limiter := concurrency.NewExecutionLimiter(int64(cfg.MaxConcurrentExecutions))

	// 1. Create shared Sandbox instance
	sb := sandbox.NewContainerdSandbox(cfg.ContainerdSocket, cfg.ContainerdNamespace)

	// 2. Create CompileCache instance (not a global singleton)
	cacheDir := filepath.Join(os.TempDir(), "afterglow-compile-cache")
	compileCache, err := cache.NewCompileCache(cacheDir, 500)
	if err != nil {
		slog.Warn("failed to initialize compile cache", "error", err)
		compileCache = nil // Allow running without cache
	}

	// 3. Inject dependencies into Runner and Compiler
	runner := service.NewContainerdRunner(sb)
	compiler := service.NewContainerCompiler(sb, compileCache)
	baseJudge := service.NewJudgeEngine(runner, compiler)

	judge := &limitedJudgeService{
		judge:   baseJudge,
		limiter: limiter,
	}

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

type limitedJudgeService struct {
	judge   service.JudgeService
	limiter *concurrency.ExecutionLimiter
}

func (s *limitedJudgeService) PreflightCheck(ctx context.Context) error {
	return s.judge.PreflightCheck(ctx)
}

func (s *limitedJudgeService) Judge(ctx context.Context, req model.JudgeRequest) model.JudgeResult {
	var result model.JudgeResult

	err := s.limiter.WithLimit(ctx, func() error {
		result = s.judge.Judge(ctx, req)
		return nil
	})
	if err != nil {
		return model.JudgeResult{
			Verdict: model.VerdictUKE,
			Compile: model.CompileResult{
				Succeeded: false,
				Log:       err.Error(),
			},
			TotalCount: len(req.TestCases),
		}
	}

	return result
}
