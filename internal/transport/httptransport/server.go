package httptransport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const httpShutdownTimeout = 10 * time.Second

// ServerOptions contains the HTTP server's runtime configuration.
type ServerOptions struct {
	Addr         string
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	MaxBodyBytes int64
}

// Server implements the HTTP transport layer.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// NewServer creates a new HTTP server.
func NewServer(opts ServerOptions, judge JudgeService, logger *slog.Logger) (*Server, error) {
	if err := validateServerOptions(opts); err != nil {
		return nil, err
	}

	handler := newHandler(judge, logger, opts.MaxBodyBytes)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/execute", handler.handleExecute)

	finalHandler := loggingMiddleware(logger)(mux)

	addr := net.JoinHostPort(strings.TrimSpace(opts.Addr), strconv.Itoa(opts.Port))
	httpServer := &http.Server{
		Addr:         addr,
		Handler:      finalHandler,
		ReadTimeout:  opts.ReadTimeout,
		WriteTimeout: opts.WriteTimeout,
	}

	return &Server{
		httpServer: httpServer,
		logger:     logger,
	}, nil
}

func validateServerOptions(opts ServerOptions) error {
	switch {
	case strings.TrimSpace(opts.Addr) == "":
		return errors.New("HTTP address is required")
	case opts.Port <= 0 || opts.Port > 65535:
		return fmt.Errorf("HTTP port must be between 1 and 65535, got %d", opts.Port)
	case opts.ReadTimeout <= 0:
		return fmt.Errorf("HTTP read timeout must be positive, got %s", opts.ReadTimeout)
	case opts.WriteTimeout <= 0:
		return fmt.Errorf("HTTP write timeout must be positive, got %s", opts.WriteTimeout)
	case opts.MaxBodyBytes <= 0:
		return fmt.Errorf("HTTP request body limit must be positive, got %d bytes", opts.MaxBodyBytes)
	default:
		return nil
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
