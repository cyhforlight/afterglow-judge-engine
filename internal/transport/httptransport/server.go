package httptransport

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"afterglow-judge-engine/internal/config"
)

const (
	httpShutdownTimeout = 10 * time.Second
)

// Server implements the HTTP transport layer.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// NewServer creates a new HTTP server.
func NewServer(cfg *config.Config, judge JudgeService, logger *slog.Logger) *Server {
	handler := NewHandler(judge, logger, cfg.MaxInputSizeMB)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/execute", handler.HandleExecute)

	var finalHandler http.Handler = mux
	if cfg.APIKey != "" {
		finalHandler = AuthMiddleware(logger, cfg.APIKey)(finalHandler)
	}
	finalHandler = RecoveryMiddleware(logger)(finalHandler)
	finalHandler = LoggingMiddleware(logger)(finalHandler)

	addr := fmt.Sprintf("%s:%d", cfg.HTTPAddr, cfg.HTTPPort)
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      finalHandler,
		ReadTimeout:  time.Duration(cfg.HTTPReadTimeoutMs) * time.Millisecond,
		WriteTimeout: time.Duration(cfg.HTTPWriteTimeoutMs) * time.Millisecond,
	}

	return &Server{
		httpServer: httpServer,
		logger:     logger,
	}
}

// Run starts the HTTP server and blocks until the context is cancelled
// or the underlying server exits with an error.
func (s *Server) Run(ctx context.Context) error {
	s.logger.Info("starting HTTP server", "addr", s.httpServer.Addr)

	serveErrCh := make(chan error, 1)
	go func() {
		err := s.httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			serveErrCh <- fmt.Errorf("server error: %w", err)
			return
		}
		serveErrCh <- nil
	}()

	select {
	case err := <-serveErrCh:
		return err
	case <-ctx.Done():
		s.logger.Info("stopping HTTP server")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
		defer cancel()

		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown failed: %w", err)
		}

		if err := <-serveErrCh; err != nil {
			return err
		}

		s.logger.Info("HTTP server stopped")
		return nil
	}
}
