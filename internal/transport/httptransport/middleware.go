package httptransport

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// LoggingMiddleware logs all HTTP requests.
func LoggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.statusCode,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote_addr", r.RemoteAddr,
			)
		})
	}
}

// RecoveryMiddleware recovers from panics and returns 500 error.
func RecoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("panic recovered",
						"error", err,
						"path", r.URL.Path,
					)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// AuthMiddleware validates API key.
func AuthMiddleware(logger *slog.Logger, apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			auth := r.Header.Get("Authorization")
			if auth == "" {
				writeErrorResponse(w, logger, http.StatusUnauthorized, "UNAUTHORIZED", "missing Authorization header")
				return
			}

			// Extract Bearer token
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == auth {
				writeErrorResponse(w, logger, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization header must use Bearer token")
				return
			}

			// Validate token
			if token != apiKey {
				writeErrorResponse(w, logger, http.StatusUnauthorized, "UNAUTHORIZED", "invalid API key")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func writeErrorResponse(w http.ResponseWriter, logger *slog.Logger, status int, code, details string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(ErrorResponseDTO{
		Error:   http.StatusText(status),
		Code:    code,
		Details: details,
	}); err != nil && logger != nil {
		logger.Error("failed to encode response", "error", err)
	}
}
