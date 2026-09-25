package httptransport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const httpReadTimeout = 30 * time.Second

// Server implements the HTTP transport layer.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// NewServer creates a new HTTP server.
func NewServer(listenAddr string, judge JudgeService, logger *slog.Logger) *Server {
	handler := newHandler(judge, logger, maxRequestBodyBytes)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/execute", handler.handleExecute)

	return &Server{
		httpServer: &http.Server{
			Addr:        listenAddr,
			Handler:     loggingMiddleware(logger)(mux),
			ReadTimeout: httpReadTimeout,
		},
		logger: logger,
	}
}

// Run serves requests until cancellation or a server error, then waits for
// active requests to finish before returning.
func (s *Server) Run(ctx context.Context) error {
	s.logger.Info("starting HTTP server", "addr", s.httpServer.Addr)

	serveDone := make(chan struct{})
	var serveErr error
	go func() {
		defer close(serveDone)
		err := s.httpServer.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr = fmt.Errorf("server error: %w", err)
		}
	}()

	select {
	case <-serveDone:
	case <-ctx.Done():
	}
	s.logger.Info("stopping HTTP server")
	shutdownErr := s.httpServer.Shutdown(context.Background())
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("server shutdown failed: %w", shutdownErr)
	}
	<-serveDone
	s.logger.Info("HTTP server stopped")
	return errors.Join(serveErr, shutdownErr)
}
