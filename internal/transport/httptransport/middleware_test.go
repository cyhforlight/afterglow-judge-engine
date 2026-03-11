package httptransport

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestLoggingMiddleware tests the logging middleware.
func TestLoggingMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		handlerStatus int
		checkDuration bool
	}{
		{
			name:          "logs successful request",
			method:        http.MethodGet,
			path:          "/test",
			handlerStatus: http.StatusOK,
		},
		{
			name:          "measures duration",
			method:        http.MethodGet,
			path:          "/slow",
			handlerStatus: http.StatusOK,
			checkDuration: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))

			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.checkDuration {
					time.Sleep(10 * time.Millisecond)
				}
				w.WriteHeader(tt.handlerStatus)
			})

			middleware := LoggingMiddleware(logger)
			wrapped := middleware(handler)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.RemoteAddr = "127.0.0.1:12345"
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			assert.Equal(t, tt.handlerStatus, w.Code)

			logOutput := logBuf.String()
			assert.Contains(t, logOutput, "http request")
			assert.Contains(t, logOutput, tt.method)
			assert.Contains(t, logOutput, tt.path)
			assert.Contains(t, logOutput, "127.0.0.1:12345")

			if tt.checkDuration {
				assert.Contains(t, logOutput, "duration_ms")
			}
		})
	}
}

func TestLoggingMiddleware_DefaultStatusCode(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	// Handler that doesn't explicitly call WriteHeader
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	middleware := LoggingMiddleware(logger)
	wrapped := middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "status=200")
}

// TestRecoveryMiddleware tests the panic recovery middleware.
func TestRecoveryMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		shouldPanic  bool
		panicValue   any
		expectedCode int
		expectedBody string
	}{
		{
			name:         "recovers from panic",
			shouldPanic:  true,
			panicValue:   "something went wrong",
			expectedCode: http.StatusInternalServerError,
			expectedBody: "Internal Server Error",
		},
		{
			name:         "does not affect normal requests",
			shouldPanic:  false,
			expectedCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))

			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.shouldPanic {
					panic(tt.panicValue)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("success"))
			})

			middleware := RecoveryMiddleware(logger)
			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)

			if tt.shouldPanic {
				assert.Contains(t, w.Body.String(), tt.expectedBody)
				logOutput := logBuf.String()
				assert.Contains(t, logOutput, "panic recovered")
			} else {
				assert.Contains(t, w.Body.String(), "success")
			}
		})
	}
}

func TestRecoveryMiddleware_SubsequentRequests(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	panicOnce := true
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if panicOnce {
			panicOnce = false
			panic("first request panic")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	middleware := RecoveryMiddleware(logger)
	wrapped := middleware(handler)

	// First request panics
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	wrapped.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusInternalServerError, w1.Code)

	// Second request succeeds
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w2 := httptest.NewRecorder()
	wrapped.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), "ok")
}

// TestTimeoutMiddleware tests the timeout middleware.
func TestTimeoutMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		timeout       time.Duration
		handlerDelay  time.Duration
		expectTimeout bool
	}{
		{
			name:          "request completes within timeout",
			timeout:       100 * time.Millisecond,
			handlerDelay:  10 * time.Millisecond,
			expectTimeout: false,
		},
		{
			name:          "zero timeout still works",
			timeout:       0,
			handlerDelay:  0,
			expectTimeout: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.handlerDelay > 0 {
					time.Sleep(tt.handlerDelay)
				}

				// Check if context is still valid
				select {
				case <-r.Context().Done():
					return
				default:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("ok"))
				}
			})

			middleware := TimeoutMiddleware(tt.timeout)
			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			if !tt.expectTimeout {
				assert.Equal(t, http.StatusOK, w.Code)
			}
		})
	}
}

func TestTimeoutMiddleware_ContextPropagation(t *testing.T) {
	var receivedDeadline time.Time
	var hasDeadline bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedDeadline, hasDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	})

	timeout := 1 * time.Second
	middleware := TimeoutMiddleware(timeout)
	wrapped := middleware(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	assert.True(t, hasDeadline, "context should have deadline")
	assert.WithinDuration(t, time.Now().Add(timeout), receivedDeadline, 100*time.Millisecond)
}

// TestAuthMiddleware tests the authentication middleware.
//
//nolint:funlen // Comprehensive matrix for middleware behavior.
func TestAuthMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		apiKeys      []string
		authHeader   string
		expectedCode int
		expectedBody string
	}{
		{
			name:         "no api keys allows all requests",
			apiKeys:      []string{},
			authHeader:   "",
			expectedCode: http.StatusOK,
		},
		{
			name:         "missing auth header returns 401",
			apiKeys:      []string{"valid-key"},
			authHeader:   "",
			expectedCode: http.StatusUnauthorized,
			expectedBody: "Missing Authorization header",
		},
		{
			name:         "invalid format returns 401",
			apiKeys:      []string{"valid-key"},
			authHeader:   "InvalidFormat token",
			expectedCode: http.StatusUnauthorized,
			expectedBody: "Invalid Authorization format",
		},
		{
			name:         "invalid api key returns 401",
			apiKeys:      []string{"valid-key"},
			authHeader:   "Bearer invalid-key",
			expectedCode: http.StatusUnauthorized,
			expectedBody: "Invalid API key",
		},
		{
			name:         "valid api key allows request",
			apiKeys:      []string{"valid-key"},
			authHeader:   "Bearer valid-key",
			expectedCode: http.StatusOK,
		},
		{
			name:         "multiple valid keys",
			apiKeys:      []string{"key1", "key2", "key3"},
			authHeader:   "Bearer key2",
			expectedCode: http.StatusOK,
		},
		{
			name:         "empty bearer token returns 401",
			apiKeys:      []string{"valid-key"},
			authHeader:   "Bearer ",
			expectedCode: http.StatusUnauthorized,
			expectedBody: "Invalid API key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("success"))
			})

			middleware := AuthMiddleware(tt.apiKeys)
			wrapped := middleware(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)

			if tt.expectedBody != "" {
				assert.Contains(t, w.Body.String(), tt.expectedBody)
			} else if tt.expectedCode == http.StatusOK {
				assert.Contains(t, w.Body.String(), "success")
			}
		})
	}
}

// TestCORSMiddleware tests the CORS middleware.
//
//nolint:funlen // Comprehensive matrix for middleware behavior.
func TestCORSMiddleware(t *testing.T) {
	tests := []struct {
		name           string
		allowedOrigins []string
		requestOrigin  string
		method         string
		expectCORS     bool
		expectedOrigin string
	}{
		{
			name:           "wildcard origin allows all",
			allowedOrigins: []string{"*"},
			requestOrigin:  "https://example.com",
			method:         http.MethodGet,
			expectCORS:     true,
			expectedOrigin: "https://example.com",
		},
		{
			name:           "specific origin allowed",
			allowedOrigins: []string{"https://example.com"},
			requestOrigin:  "https://example.com",
			method:         http.MethodGet,
			expectCORS:     true,
			expectedOrigin: "https://example.com",
		},
		{
			name:           "specific origin denied",
			allowedOrigins: []string{"https://example.com"},
			requestOrigin:  "https://evil.com",
			method:         http.MethodGet,
			expectCORS:     false,
		},
		{
			name:           "missing origin with wildcard",
			allowedOrigins: []string{"*"},
			requestOrigin:  "",
			method:         http.MethodGet,
			expectCORS:     true,
			expectedOrigin: "*",
		},
		{
			name:           "missing origin without wildcard",
			allowedOrigins: []string{"https://example.com"},
			requestOrigin:  "",
			method:         http.MethodGet,
			expectCORS:     false,
		},
		{
			name:           "preflight request",
			allowedOrigins: []string{"*"},
			requestOrigin:  "https://example.com",
			method:         http.MethodOptions,
			expectCORS:     true,
			expectedOrigin: "https://example.com",
		},
		{
			name:           "multiple allowed origins",
			allowedOrigins: []string{"https://example.com", "https://test.com"},
			requestOrigin:  "https://test.com",
			method:         http.MethodPost,
			expectCORS:     true,
			expectedOrigin: "https://test.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("ok"))
			})

			middleware := CORSMiddleware(tt.allowedOrigins)
			wrapped := middleware(handler)

			req := httptest.NewRequest(tt.method, "/test", nil)
			if tt.requestOrigin != "" {
				req.Header.Set("Origin", tt.requestOrigin)
			}
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			if tt.expectCORS {
				assert.Equal(t, tt.expectedOrigin, w.Header().Get("Access-Control-Allow-Origin"))
				assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"))
				assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
			} else {
				assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
			}

			if tt.method == http.MethodOptions {
				assert.Equal(t, http.StatusOK, w.Code)
			}
		})
	}
}
