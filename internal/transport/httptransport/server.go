package httptransport

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"afterglow-judge-engine/internal/config"
	"afterglow-judge-engine/internal/service"
)

// Server implements the HTTP transport layer.
type Server struct {
	httpServer *http.Server
	handler    *Handler
	logger     *slog.Logger
	addr       string
}

// NewServer creates a new HTTP server.
func NewServer(cfg *config.Config, judge service.JudgeService, logger *slog.Logger) *Server {
	handler := NewHandler(judge, logger, cfg.MaxInputSizeMB)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/execute", handler.HandleExecute)
	mux.HandleFunc("GET /health", handler.HandleHealth)

	var finalHandler http.Handler = mux
	if len(cfg.APIKeys) > 0 {
		finalHandler = AuthMiddleware(logger, cfg.APIKeys)(finalHandler)
	}
	finalHandler = LoggingMiddleware(logger)(finalHandler)
	finalHandler = RecoveryMiddleware(logger)(finalHandler)

	httpServer := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      finalHandler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	return &Server{
		httpServer: httpServer,
		handler:    handler,
		logger:     logger,
		addr:       cfg.Addr(),
	}
}

// Start starts the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	s.logger.Info("starting HTTP server", "addr", s.addr)

	errChan := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		return s.Stop(context.Background())
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	}
}

// Stop gracefully shuts down the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("stopping HTTP server")
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown failed: %w", err)
	}
	s.logger.Info("HTTP server stopped")
	return nil
}

// Addr returns the server's listening address.
func (s *Server) Addr() string {
	return s.addr
}
