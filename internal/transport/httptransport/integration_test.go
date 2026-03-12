package httptransport

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"afterglow-judge-engine/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_HTTPServer_FullLifecycle(t *testing.T) {
	cfg := &config.Config{
		HTTPAddr:       "localhost",
		HTTPPort:       0,
		MaxInputSizeMB: 256,
	}

	judge := &mockJudgeService{}
	server := NewServer(cfg, judge, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	assert.NotNil(t, server.httpServer)

	cancel()
	select {
	case err := <-errChan:
		if err != nil && strings.Contains(err.Error(), "operation not permitted") {
			t.Skip("listening sockets are not permitted in this sandbox")
		}
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func TestIntegration_NewServer_UsesAPIKeyForAuth(t *testing.T) {
	cfg := &config.Config{
		HTTPAddr:       "localhost",
		HTTPPort:       8080,
		MaxInputSizeMB: 256,
		APIKey:         "secret-token",
	}

	server := NewServer(cfg, &mockJudgeService{}, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp ErrorResponseDTO
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "UNAUTHORIZED", resp.Code)
	assert.Equal(t, "missing Authorization header", resp.Details)
}
