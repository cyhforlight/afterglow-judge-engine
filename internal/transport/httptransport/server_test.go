package httptransport

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"afterglow-judge-engine/internal/config"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/require"
)

func TestServer_GracefulShutdown(t *testing.T) {
	cfg := testServerConfig()

	server := NewServer(cfg, &mockJudgeService{}, slog.Default())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errChan:
		if err != nil && strings.Contains(err.Error(), "operation not permitted") {
			t.Skip("listening sockets are not permitted in this sandbox")
		}
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}

func testServerConfig() *config.Config {
	return &config.Config{
		HTTPAddr:           "localhost",
		HTTPPort:           0,
		HTTPReadTimeoutMs:  30_000,
		HTTPWriteTimeoutMs: 120_000,
		MaxInputSizeMB:     256,
		JudgeLimits:        model.DefaultJudgeLimits(),
	}
}
