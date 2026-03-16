package httptransport

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"afterglow-judge-engine/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_StartStop(t *testing.T) {
	cfg := &config.Config{
		HTTPAddr:       "localhost",
		HTTPPort:       0,
		MaxInputSizeMB: 256,
	}

	server := NewServer(cfg, &mockJudgeService{}, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
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
		t.Fatal("server did not stop in time")
	}
}

func TestServer_GracefulShutdown(t *testing.T) {
	cfg := &config.Config{
		HTTPAddr:       "localhost",
		HTTPPort:       0,
		MaxInputSizeMB: 256,
	}

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
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}
