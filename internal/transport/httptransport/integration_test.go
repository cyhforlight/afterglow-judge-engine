package httptransport

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"afterglow-judge-engine/internal/config"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIntegration_NewServer_UsesAPIKeyForAuth(t *testing.T) {
	cfg := integrationTestConfig()
	cfg.HTTPPort = 8080
	cfg.APIKey = "secret-token"

	server := NewServer(cfg, &mockJudgeService{}, slog.Default())

	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	w := httptest.NewRecorder()

	server.httpServer.Handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var resp errorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "UNAUTHORIZED", resp.Code)
	assert.Equal(t, "missing Authorization header", resp.Details)
}

func integrationTestConfig() *config.Config {
	return &config.Config{
		HTTPAddr:           "localhost",
		HTTPPort:           0,
		HTTPReadTimeoutMs:  30_000,
		HTTPWriteTimeoutMs: 120_000,
		MaxInputSizeMB:     256,
		JudgeLimits:        model.DefaultJudgeLimits(),
	}
}
