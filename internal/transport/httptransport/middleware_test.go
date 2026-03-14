package httptransport

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

			wrapped := LoggingMiddleware(logger)(handler)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			assert.Equal(t, tt.handlerStatus, w.Code)

			logOutput := logBuf.String()
			assert.Contains(t, logOutput, "http request")
			assert.Contains(t, logOutput, tt.method)
			assert.Contains(t, logOutput, tt.path)

			if tt.checkDuration {
				assert.Contains(t, logOutput, "duration_ms")
			}
		})
	}
}

func TestLoggingMiddleware_DefaultStatusCode(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	wrapped := LoggingMiddleware(logger)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	assert.Contains(t, logBuf.String(), "status=200")
}

func TestRecoveryMiddleware(t *testing.T) {
	tests := []struct {
		name         string
		shouldPanic  bool
		panicValue   any
		expectedCode int
	}{
		{
			name:         "recovers from panic",
			shouldPanic:  true,
			panicValue:   "something went wrong",
			expectedCode: http.StatusInternalServerError,
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

			wrapped := RecoveryMiddleware(logger)(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)

			if tt.shouldPanic {
				assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
				var resp ErrorResponseDTO
				err := json.NewDecoder(w.Body).Decode(&resp)
				require.NoError(t, err)
				assert.Equal(t, "INTERNAL_ERROR", resp.Code)
				assert.Contains(t, logBuf.String(), "panic recovered")
				return
			}

			assert.Contains(t, w.Body.String(), "success")
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

	wrapped := RecoveryMiddleware(logger)(handler)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w1 := httptest.NewRecorder()
	wrapped.ServeHTTP(w1, req1)
	assert.Equal(t, http.StatusInternalServerError, w1.Code)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	w2 := httptest.NewRecorder()
	wrapped.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Contains(t, w2.Body.String(), "ok")
}

func TestAuthMiddleware(t *testing.T) {
	tests := []struct {
		name          string
		apiKey        string
		authHeader    string
		expectedCode  int
		expectedError ErrorResponseDTO
	}{
		{
			name:         "no api key allows all requests",
			apiKey:       "",
			expectedCode: http.StatusOK,
		},
		{
			name:         "missing auth header returns 401",
			apiKey:       "valid-key",
			expectedCode: http.StatusUnauthorized,
			expectedError: ErrorResponseDTO{
				Error:   http.StatusText(http.StatusUnauthorized),
				Code:    "UNAUTHORIZED",
				Details: "missing Authorization header",
			},
		},
		{
			name:         "invalid format returns 401",
			apiKey:       "valid-key",
			authHeader:   "InvalidFormat token",
			expectedCode: http.StatusUnauthorized,
			expectedError: ErrorResponseDTO{
				Error:   http.StatusText(http.StatusUnauthorized),
				Code:    "UNAUTHORIZED",
				Details: "Authorization header must use Bearer token",
			},
		},
		{
			name:         "invalid api key returns 401",
			apiKey:       "valid-key",
			authHeader:   "Bearer invalid-key",
			expectedCode: http.StatusUnauthorized,
			expectedError: ErrorResponseDTO{
				Error:   http.StatusText(http.StatusUnauthorized),
				Code:    "UNAUTHORIZED",
				Details: "invalid API key",
			},
		},
		{
			name:         "valid api key allows request",
			apiKey:       "valid-key",
			authHeader:   "Bearer valid-key",
			expectedCode: http.StatusOK,
		},
		{
			name:         "empty bearer token returns 401",
			apiKey:       "valid-key",
			authHeader:   "Bearer ",
			expectedCode: http.StatusUnauthorized,
			expectedError: ErrorResponseDTO{
				Error:   http.StatusText(http.StatusUnauthorized),
				Code:    "UNAUTHORIZED",
				Details: "invalid API key",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logBuf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logBuf, nil))

			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("success"))
			})

			wrapped := AuthMiddleware(logger, tt.apiKey)(handler)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()

			wrapped.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)

			if tt.expectedCode == http.StatusOK {
				assert.Contains(t, w.Body.String(), "success")
				return
			}

			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

			var resp ErrorResponseDTO
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedError, resp)
		})
	}
}
