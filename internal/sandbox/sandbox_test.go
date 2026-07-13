package sandbox

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTaskController struct {
	start   func(context.Context) error
	kill    func(context.Context, syscall.Signal, ...containerd.KillOpts) error
	metrics func(context.Context) (*types.Metric, error)
}

func (f *fakeTaskController) Start(ctx context.Context) error {
	if f.start == nil {
		return nil
	}
	return f.start(ctx)
}

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

func TestValidateExecuteLimits(t *testing.T) {
	tests := []struct {
		name    string
		limits  ResourceLimits
		wantErr string
	}{
		{
			name: "valid limits",
			limits: ResourceLimits{
				CPUTimeMs:   1000,
				WallTimeMs:  3000,
				MemoryMB:    256,
				OutputBytes: 1024,
			},
		},
		{
			name: "cpu time must be positive",
			limits: ResourceLimits{
				CPUTimeMs:   0,
				WallTimeMs:  3000,
				MemoryMB:    256,
				OutputBytes: 1024,
			},
			wantErr: "CPU time limit must be positive",
		},
		{
			name: "wall time must be positive",
			limits: ResourceLimits{
				CPUTimeMs:   1000,
				WallTimeMs:  0,
				MemoryMB:    256,
				OutputBytes: 1024,
			},
			wantErr: "wall time limit must be positive",
		},
		{
			name: "memory must be positive",
			limits: ResourceLimits{
				CPUTimeMs:   1000,
				WallTimeMs:  3000,
				MemoryMB:    0,
				OutputBytes: 1024,
			},
			wantErr: "memory limit must be positive",
		},
		{
			name: "output must be positive",
			limits: ResourceLimits{
				CPUTimeMs:   1000,
				WallTimeMs:  3000,
				MemoryMB:    256,
				OutputBytes: 0,
			},
			wantErr: "output limit must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateExecuteLimits(tt.limits)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}

func TestResolveCwd(t *testing.T) {
	tests := []struct {
		name    string
		req     ExecuteRequest
		want    string
		wantOK  bool
		wantErr bool
	}{
		{
			name:   "explicit cwd wins",
			req:    ExecuteRequest{MountDir: &Mount{ContainerPath: "/sandbox"}, Cwd: stringPtr("/work")},
			want:   "/work",
			wantOK: true,
		},
		{
			name:   "mount dir becomes default cwd",
			req:    ExecuteRequest{MountDir: &Mount{ContainerPath: "/sandbox"}},
			want:   "/sandbox",
			wantOK: true,
		},
		{
			name:   "no mount and no cwd uses image default",
			req:    ExecuteRequest{},
			wantOK: false,
		},
		{
			name:    "relative cwd is rejected",
			req:     ExecuteRequest{Cwd: stringPtr("sandbox")},
			wantErr: true,
		},
		{
			name:    "relative mount path is rejected",
			req:     ExecuteRequest{MountDir: &Mount{ContainerPath: "sandbox"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := resolveCwd(tt.req)
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWatchExecution_CancellationStopsTask(t *testing.T) {
	baseCtx := namespaces.WithNamespace(context.Background(), "test-namespace")
	ctx, cancel := context.WithCancel(baseCtx)
	started := make(chan struct{})
	exitCh := make(chan containerd.ExitStatus, 1)

	var killContextErr error
	var killNamespace string
	var killAll bool
	var killSignal syscall.Signal
	var killOptErr error
	task := &fakeTaskController{
		start: func(context.Context) error {
			close(started)
			return nil
		},
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

	type executionOutcome struct {
		result ExecuteResult
		err    error
	}
	outcomeCh := make(chan executionOutcome, 1)
	go func() {
		limiter := newOutputLimiter(1024)
		result, err := (&ContainerdSandbox{}).watchExecution(
			ctx,
			task,
			exitCh,
			newLimitedWriter(limiter),
			newLimitedWriter(limiter),
			limiter,
			standardLimits(),
		)
		outcomeCh <- executionOutcome{result: result, err: err}
	}()

	<-started
	cancel()

	select {
	case outcome := <-outcomeCh:
		assert.Equal(t, ExecuteResult{}, outcome.result)
		require.ErrorIs(t, outcome.err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("watchExecution did not return after cancellation")
	}

	require.NoError(t, killContextErr)
	assert.Equal(t, "test-namespace", killNamespace)
	assert.Equal(t, syscall.SIGKILL, killSignal)
	require.NoError(t, killOptErr)
	assert.True(t, killAll)
}

func TestWatchExecution_NaturalExitReturnsMetricsError(t *testing.T) {
	exitCh := make(chan containerd.ExitStatus, 1)
	exitCh <- *containerd.NewExitStatus(0, time.Now(), nil)
	limiter := newOutputLimiter(1024)

	result, err := (&ContainerdSandbox{}).watchExecution(
		context.Background(),
		&fakeTaskController{},
		exitCh,
		newLimitedWriter(limiter),
		newLimitedWriter(limiter),
		limiter,
		standardLimits(),
	)

	assert.Equal(t, ExecuteResult{}, result)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collect cgroup metrics")
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

	result, err := (&ContainerdSandbox{}).watchExecution(
		context.Background(),
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

func TestStopTask_WaitIsBounded(t *testing.T) {
	baseCtx := namespaces.WithNamespace(context.Background(), "test-namespace")
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
}

func TestRunCleanupActions_UsesIndependentContextInReverseOrder(t *testing.T) {
	baseCtx := namespaces.WithNamespace(context.Background(), "test-namespace")
	ctx, cancel := context.WithCancel(baseCtx)
	cancel()

	var order []string
	contextsActive := true
	namespacesPreserved := true
	action := func(name string) cleanupAction {
		return cleanupAction{
			resource: name,
			run: func(cleanupCtx context.Context) error {
				order = append(order, name)
				contextsActive = contextsActive && cleanupCtx.Err() == nil
				namespace, ok := namespaces.Namespace(cleanupCtx)
				namespacesPreserved = namespacesPreserved && ok && namespace == "test-namespace"
				return nil
			},
		}
	}

	runCleanupActions(ctx, []cleanupAction{action("container"), action("task")})

	assert.Equal(t, []string{"task", "container"}, order)
	assert.True(t, contextsActive)
	assert.True(t, namespacesPreserved)
}

func stringPtr(val string) *string {
	return &val
}
