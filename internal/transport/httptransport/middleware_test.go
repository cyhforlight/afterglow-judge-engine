package httptransport

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecoveryMiddleware_ReturnsJSON(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	handler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("something went wrong")
	})

	wrapped := RecoveryMiddleware(logger)(handler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	wrapped.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp ErrorResponseDTO
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "INTERNAL_ERROR", resp.Code)
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
