package httptransport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	httpReadTimeout     = 30 * time.Second
	httpShutdownTimeout = 10 * time.Second
)

// Server implements the HTTP transport layer.
type Server struct {
	httpServer     *http.Server
	logger         *slog.Logger
	requestCtx     context.Context
	cancelRequests context.CancelFunc
	requestMu      sync.Mutex
	requests       sync.WaitGroup
}

// NewServer creates a new HTTP server.
func NewServer(listenAddr string, judge JudgeService, logger *slog.Logger) *Server {
	handler := newHandler(judge, logger, maxRequestBodyBytes)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/execute", handler.handleExecute)

	finalHandler := loggingMiddleware(logger)(mux)
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	server := &Server{
		logger:         logger,
		requestCtx:     requestCtx,
		cancelRequests: cancelRequests,
	}

	server.httpServer = &http.Server{
		Addr: listenAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server.requestMu.Lock()
			if server.requestCtx.Err() != nil {
				server.requestMu.Unlock()
				handler.writeJSON(w, http.StatusServiceUnavailable, errorResponse{
					Error: http.StatusText(http.StatusServiceUnavailable),
					Code:  "SERVER_SHUTTING_DOWN",
				})
				return
			}
			server.requests.Add(1)
			server.requestMu.Unlock()
			defer server.requests.Done()
			finalHandler.ServeHTTP(w, r)
		}),
		ReadTimeout: httpReadTimeout,
		BaseContext: func(net.Listener) context.Context { return requestCtx },
	}

	return server
}

// Run starts the HTTP server and blocks until the context is cancelled
// or the underlying server exits with an error.
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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), httpShutdownTimeout)
	defer cancel()
	shutdownErr := s.shutdown(shutdownCtx)
	<-serveDone
	s.logger.Info("HTTP server stopped")
	return errors.Join(serveErr, shutdownErr)
}

func (s *Server) shutdown(graceCtx context.Context) error {
	shutdownErr := s.httpServer.Shutdown(graceCtx)

	// Close admission before waiting: a handler may have been dispatched just
	// before Shutdown stopped the listener but not registered itself yet.
	s.requestMu.Lock()
	s.cancelRequests()
	s.requestMu.Unlock()
	if shutdownErr != nil {
		// Cancellation releases judge work; closing connections also releases
		// handlers blocked on client reads or response writes.
		shutdownErr = errors.Join(shutdownErr, s.httpServer.Close())
	}
	s.requests.Wait()
	if shutdownErr != nil {
		return fmt.Errorf("server shutdown failed: %w", shutdownErr)
	}
	return nil
}
