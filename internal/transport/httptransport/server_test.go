package httptransport

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"

	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/require"
)

type judgeFunc func(context.Context) (model.JudgeResult, error)

func (judge judgeFunc) Judge(ctx context.Context, _ model.JudgeRequest) (model.JudgeResult, error) {
	return judge(ctx)
}

func TestServerRunWaitsForJudging(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		name := "connected client"
		if disconnect {
			name = "disconnected client"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan context.Context, 1)
			release := make(chan struct{})
			finishRequest := sync.OnceFunc(func() { close(release) })
			server := NewServer("127.0.0.1:0", judgeFunc(func(ctx context.Context) (model.JudgeResult, error) {
				started <- ctx
				<-release
				return model.JudgeResult{}, nil
			}), slog.New(slog.DiscardHandler))

			listening := make(chan string, 1)
			server.httpServer.BaseContext = func(listener net.Listener) context.Context {
				listening <- listener.Addr().String()
				return t.Context()
			}
			requestContexts := make(chan context.Context, 1)
			handler := server.httpServer.Handler
			server.httpServer.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestContexts <- r.Context()
				handler.ServeHTTP(w, r)
			})
			shutdownStarted := make(chan struct{})
			server.httpServer.RegisterOnShutdown(func() { close(shutdownStarted) })
			runCtx, stopServer := context.WithCancel(t.Context())
			runDone := make(chan struct{})
			var runErr error
			go func() {
				runErr = server.Run(runCtx)
				close(runDone)
			}()
			t.Cleanup(func() {
				finishRequest()
				stopServer()
				<-runDone
			})

			clientCtx, cancelRequest := context.WithCancel(t.Context())
			defer cancelRequest()
			request, err := http.NewRequestWithContext(clientCtx, http.MethodPost,
				"http://"+<-listening+"/v1/execute", makeJudgeBody(t, validJudgeRequest()))
			require.NoError(t, err)
			request.Header.Set("Content-Type", "application/json")
			requestDone := make(chan error, 1)
			go func() {
				response, err := http.DefaultClient.Do(request)
				if err == nil {
					_, err = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
				requestDone <- err
			}()
			requestCtx := <-requestContexts
			judgeCtx := <-started
			if disconnect {
				cancelRequest()
				require.ErrorIs(t, <-requestDone, context.Canceled)
				<-requestCtx.Done()
			}

			stopServer()
			<-shutdownStarted
			require.NoError(t, judgeCtx.Err())
			select {
			case <-runDone:
				t.Error("server returned before judging finished")
			default:
			}
			finishRequest()
			<-runDone
			require.NoError(t, runErr)
			if !disconnect {
				require.NoError(t, <-requestDone)
			}
		})
	}
}
