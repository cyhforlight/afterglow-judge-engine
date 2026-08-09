package sandbox

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTaskController struct {
	kill    func(context.Context, syscall.Signal, ...containerd.KillOpts) error
	metrics func(context.Context) (*types.Metric, error)
}

func (*fakeTaskController) Start(context.Context) error { return nil }

func (*fakeTaskController) CloseIO(context.Context, ...containerd.IOCloserOpts) error {
	return nil
}

func (f *fakeTaskController) Kill(ctx context.Context, signal syscall.Signal, opts ...containerd.KillOpts) error {
	if f.kill == nil {
		return nil
	}
	return f.kill(ctx, signal, opts...)
}

func (f *fakeTaskController) Metrics(ctx context.Context) (*types.Metric, error) {
	if f.metrics != nil {
		return f.metrics(ctx)
	}
	return nil, errors.New("metrics unavailable in lifecycle test")
}

func TestWatchExecution_CancellationStopsTask(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		baseCtx := namespaces.WithNamespace(t.Context(), "test-namespace")
		ctx, cancel := context.WithCancel(baseCtx)
		defer cancel()
		exitCh := make(chan containerd.ExitStatus, 1)

		var killContextErr error
		var killNamespace string
		var killAll bool
		var killSignal syscall.Signal
		var killOptErr error
		task := &fakeTaskController{
			kill: func(killCtx context.Context, signal syscall.Signal, opts ...containerd.KillOpts) error {
				killContextErr = killCtx.Err()
				killNamespace, _ = namespaces.Namespace(killCtx)
				killSignal = signal

				var info containerd.KillInfo
				for _, opt := range opts {
					if err := opt(killCtx, &info); err != nil {
						killOptErr = err
						return err
					}
				}
				killAll = info.All
				exitCh <- *containerd.NewExitStatus(137, time.Now(), nil)
				return nil
			},
		}

		var result ExecuteResult
		var watchErr error
		go func() {
			limiter := newOutputLimiter(1024)
			result, watchErr = (&Sandbox{}).watchExecution(
				ctx,
				task,
				exitCh,
				newLimitedWriter(limiter),
				newLimitedWriter(limiter),
				limiter,
				standardLimits(),
			)
		}()

		synctest.Wait()
		cancel()
		synctest.Wait()

		assert.Equal(t, ExecuteResult{}, result)
		require.ErrorIs(t, watchErr, context.Canceled)
		require.NoError(t, killContextErr)
		assert.Equal(t, "test-namespace", killNamespace)
		assert.Equal(t, syscall.SIGKILL, killSignal)
		require.NoError(t, killOptErr)
		assert.True(t, killAll)
	})
}

func TestWatchExecution_ForcedStopStillKillsTaskWhenMetricsFail(t *testing.T) {
	metricsErr := errors.New("metrics unavailable")
	exitCh := make(chan containerd.ExitStatus, 1)
	killed := false
	task := &fakeTaskController{
		metrics: func(context.Context) (*types.Metric, error) {
			return nil, metricsErr
		},
		kill: func(context.Context, syscall.Signal, ...containerd.KillOpts) error {
			killed = true
			exitCh <- *containerd.NewExitStatus(137, time.Now(), nil)
			return nil
		},
	}
	limiter := newOutputLimiter(1024)
	limiter.signal()

	result, err := (&Sandbox{}).watchExecution(
		t.Context(),
		task,
		exitCh,
		newLimitedWriter(limiter),
		newLimitedWriter(limiter),
		limiter,
		standardLimits(),
	)

	assert.Equal(t, ExecuteResult{}, result)
	require.ErrorIs(t, err, metricsErr)
	assert.True(t, killed)
}

func TestWatchExecution_CPUMetricsFailureStopsTask(t *testing.T) {
	metricsErr := errors.New("CPU metrics unavailable")
	exitCh := make(chan containerd.ExitStatus, 1)
	killed := false
	task := &fakeTaskController{
		metrics: func(context.Context) (*types.Metric, error) {
			return nil, metricsErr
		},
		kill: func(context.Context, syscall.Signal, ...containerd.KillOpts) error {
			killed = true
			exitCh <- *containerd.NewExitStatus(137, time.Now(), nil)
			return nil
		},
	}
	limiter := newOutputLimiter(1024)
	limits := standardLimits()
	limits.WallTimeMs = 1000

	result, err := (&Sandbox{}).watchExecution(
		t.Context(),
		task,
		exitCh,
		newLimitedWriter(limiter),
		newLimitedWriter(limiter),
		limiter,
		limits,
	)

	assert.Equal(t, ExecuteResult{}, result)
	require.ErrorIs(t, err, metricsErr)
	assert.True(t, killed)
}

func TestStopTask_WaitIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		baseCtx := namespaces.WithNamespace(t.Context(), "test-namespace")
		ctx, cancel := context.WithCancel(baseCtx)
		cancel()

		var killContextErr error
		var killNamespace string
		task := &fakeTaskController{
			kill: func(killCtx context.Context, _ syscall.Signal, _ ...containerd.KillOpts) error {
				killContextErr = killCtx.Err()
				killNamespace, _ = namespaces.Namespace(killCtx)
				return nil
			},
		}

		err := stopTask(ctx, task, make(chan containerd.ExitStatus), 10*time.Millisecond)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.NoError(t, killContextErr)
		assert.Equal(t, "test-namespace", killNamespace)
	})
}
