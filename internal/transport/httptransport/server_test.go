package httptransport

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type judgeFunc func(context.Context) (model.JudgeResult, error)

func (judge judgeFunc) Judge(ctx context.Context, _ model.JudgeRequest) (model.JudgeResult, error) {
	return judge(ctx)
}

func startServerRequest(t *testing.T, judge JudgeService) (*Server, <-chan error) {
	t.Helper()
	server := NewServer("", judge, slog.New(slog.DiscardHandler))
	endpoint := httptest.NewUnstartedServer(server.httpServer.Handler)
	endpoint.Config = server.httpServer
	endpoint.Start()
	t.Cleanup(endpoint.Close)

	body := makeJudgeBody(t, validJudgeRequest())
	finished := make(chan error, 1)
	go func() {
		response, err := endpoint.Client().Post(endpoint.URL+"/v1/execute", "application/json", body)
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		finished <- err
	}()
	return server, finished
}

func TestServerShutdownAllowsRequestsToFinishDuringGrace(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	finishRequest := sync.OnceFunc(func() { close(release) })
	server, requestDone := startServerRequest(t, judgeFunc(func(ctx context.Context) (model.JudgeResult, error) {
		started <- ctx
		<-release
		return model.JudgeResult{}, nil
	}))
	t.Cleanup(finishRequest)
	requestCtx := <-started

	graceCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	graceStarted := make(chan struct{})
	server.httpServer.RegisterOnShutdown(func() { close(graceStarted) })
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.shutdown(graceCtx) }()

	// Shutdown has stopped accepting connections, but the active request is
	// still allowed to finish normally before its context is cancelled.
	<-graceStarted
	require.NoError(t, requestCtx.Err())
	finishRequest()
	require.NoError(t, <-shutdownDone)
	require.NoError(t, <-requestDone)
}

func TestServerShutdownCancelsAndWaitsForRequestCleanup(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	cleanup := make(chan struct{})
	finishCleanup := sync.OnceFunc(func() { close(cleanup) })
	server, requestDone := startServerRequest(t, judgeFunc(func(ctx context.Context) (model.JudgeResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-cleanup
		return model.JudgeResult{}, nil
	}))
	t.Cleanup(finishCleanup)
	<-started

	graceCtx, endGrace := context.WithCancel(t.Context())
	endGrace()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.shutdown(graceCtx) }()
	<-cancelled
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before request cleanup: %v", err)
	default:
	}
	finishCleanup()
	require.ErrorIs(t, <-shutdownDone, context.Canceled)
	<-requestDone

	lateRequest := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, validJudgeRequest()))
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, lateRequest)
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
}
