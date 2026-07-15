package httptransport

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_NewServer_UsesAPIKeyForAuth(t *testing.T) {
	opts := testServerOptions()
	opts.APIKey = "secret-token"

	server, err := NewServer(opts, &mockJudgeService{}, slog.Default())
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", http.NoBody)
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp errorResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "UNAUTHORIZED", resp.Code)
	assert.Equal(t, "missing Authorization header", resp.Details)
}

func TestNewServer_FormatsIPv6Address(t *testing.T) {
	opts := testServerOptions()
	opts.Addr = "::1"

	server, err := NewServer(opts, &mockJudgeService{}, slog.Default())

	require.NoError(t, err)
	assert.Equal(t, "[::1]:8080", server.httpServer.Addr)
}

func TestNewServer_RejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ServerOptions)
		wantErr string
	}{
		{name: "missing address", mutate: func(opts *ServerOptions) { opts.Addr = "" }, wantErr: "HTTP address is required"},
		{name: "invalid port", mutate: func(opts *ServerOptions) { opts.Port = 0 }, wantErr: "HTTP port must be between"},
		{name: "invalid read timeout", mutate: func(opts *ServerOptions) { opts.ReadTimeout = 0 }, wantErr: "HTTP read timeout must be positive"},
		{name: "invalid write timeout", mutate: func(opts *ServerOptions) { opts.WriteTimeout = 0 }, wantErr: "HTTP write timeout must be positive"},
		{name: "invalid body limit", mutate: func(opts *ServerOptions) { opts.MaxBodyBytes = 0 }, wantErr: "HTTP request body limit must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := testServerOptions()
			tt.mutate(&opts)

			server, err := NewServer(opts, &mockJudgeService{}, slog.Default())
			assert.Nil(t, server)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestNewServer_RequiresDependencies(t *testing.T) {
	server, err := NewServer(testServerOptions(), nil, slog.Default())
	assert.Nil(t, server)
	require.EqualError(t, err, "judge service is required")

	server, err = NewServer(testServerOptions(), &mockJudgeService{}, nil)
	assert.Nil(t, server)
	require.EqualError(t, err, "logger is required")
}

func testServerOptions() ServerOptions {
	return ServerOptions{
		Addr:         "localhost",
		Port:         8080,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 2 * time.Minute,
		MaxBodyBytes: 256 * testBytesPerMiB,
	}
}
