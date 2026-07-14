package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
)

const (
	defaultSocketPath         = "/run/containerd/containerd.sock"
	defaultNamespace          = "afterglow"
	lifecycleOperationTimeout = 5 * time.Second
	cpuTimeCheckInterval      = 10 * time.Millisecond
	cpuTimeLimitReason        = "CPU time limit exceeded"
	outputLimitReason         = "output limit exceeded"
	wallTimeLimitReason       = "wall time limit exceeded"
)

type taskController interface {
	metricsReader
	Start(context.Context) error
	CloseIO(context.Context, ...containerd.IOCloserOpts) error
	Kill(context.Context, syscall.Signal, ...containerd.KillOpts) error
}

type executionEvent struct {
	status  containerd.ExitStatus
	exited  bool
	reason  string
	metrics cgroupMetrics
	err     error
}

// Sandbox executes commands in isolated containerd containers.
type Sandbox struct {
	socketPath string
	namespace  string
	cpus       cpuPool
}

// New creates a containerd-based sandbox.
func New(socketPath, namespace string) (*Sandbox, error) {
	if socketPath == "" {
		socketPath = defaultSocketPath
	}
	if namespace == "" {
		namespace = defaultNamespace
	}

	cpus, err := newCPUPool()
	if err != nil {
		return nil, err
	}

	return &Sandbox{
		socketPath: socketPath,
		namespace:  namespace,
		cpus:       cpus,
	}, nil
}

// CheckReadiness verifies that cgroup v2 and containerd are available.
func (s *Sandbox) CheckReadiness(ctx context.Context) error {
	if err := ensureCgroupV2Enabled(); err != nil {
		return err
	}
	if err := ensureContainerdAvailable(ctx, s.socketPath); err != nil {
		return err
	}
	slog.DebugContext(ctx, "sandbox readiness checks passed")
	return nil
}

// Execute runs a command in an isolated container.
func (s *Sandbox) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if err := validateExecuteLimits(req.Limits); err != nil {
		return ExecuteResult{}, err
	}

	client, err := containerd.New(s.socketPath)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("connect to containerd: %w", err)
	}
	defer func() { _ = client.Close() }()

	execCtx := namespaces.WithNamespace(ctx, s.namespace)

	image, err := s.ensureImage(execCtx, client, req.ImageRef)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("ensure image %q: %w", req.ImageRef, err)
	}

	return s.executeInContainer(execCtx, client, image, req)
}

func validateExecuteLimits(limits ResourceLimits) error {
	switch {
	case limits.CPUTimeMs <= 0:
		return errors.New("CPU time limit must be positive")
	case limits.WallTimeMs <= 0:
		return errors.New("wall time limit must be positive")
	case limits.MemoryMB <= 0:
		return errors.New("memory limit must be positive")
	case limits.OutputBytes <= 0:
		return errors.New("output limit must be positive")
	default:
		return nil
	}
}

func (*Sandbox) ensureImage(ctx context.Context, client *containerd.Client, imageRef string) (containerd.Image, error) {
	image, err := client.GetImage(ctx, imageRef)
	if err == nil {
		slog.DebugContext(ctx, "image found locally", "ref", imageRef)
		return image, nil
	}
	if !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("get image %q: %w", imageRef, err)
	}
	slog.InfoContext(ctx, "pulling image", "ref", imageRef)
	image, err = client.Pull(ctx, imageRef, containerd.WithPullUnpack)
	if err != nil {
		return nil, fmt.Errorf("pull image %q: %w", imageRef, err)
	}
	return image, nil
}

func (s *Sandbox) executeInContainer(
	ctx context.Context,
	client *containerd.Client,
	image containerd.Image,
	req ExecuteRequest,
) (ExecuteResult, error) {
	cpuID, err := s.cpus.acquire(ctx)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("acquire CPU: %w", err)
	}
	defer s.cpus.release(cpuID)

	containerID := generateContainerID()
	requestSpecOpts, err := sandboxSpecOpts(req, cpuID)
	if err != nil {
		return ExecuteResult{}, err
	}

	specOpts := make([]oci.SpecOpts, 0, 2+len(requestSpecOpts))
	specOpts = append(specOpts,
		oci.WithImageConfig(image),
		oci.WithProcessArgs(req.Command...),
	)
	specOpts = append(specOpts, requestSpecOpts...)

	container, err := client.NewContainer(ctx, containerID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(containerID+"-snap", image),
		containerd.WithNewSpec(specOpts...),
	)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("create container: %w", err)
	}
	defer cleanupResource(ctx, "container and snapshot", func(cleanupCtx context.Context) error {
		return container.Delete(cleanupCtx, containerd.WithSnapshotCleanup)
	})

	slog.DebugContext(ctx, "container created", "id", containerID, "image", req.ImageRef)

	oleLimiter := newOutputLimiter(req.Limits.OutputBytes)
	stdoutLW := newLimitedWriter(oleLimiter)
	stderrLW := newLimitedWriter(oleLimiter)

	stdin := req.Stdin
	if stdin == nil {
		stdin = bytes.NewReader(nil)
	}

	task, err := container.NewTask(ctx, cio.NewCreator(
		cio.WithStreams(stdin, stdoutLW, stderrLW),
	))
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("create task: %w", err)
	}
	defer cleanupResource(ctx, "task", func(cleanupCtx context.Context) error {
		_, err := task.Delete(cleanupCtx, containerd.WithProcessKill)
		return err
	})

	waitCtx, cancelWait := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWait()

	exitCh, err := task.Wait(waitCtx)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("setup wait: %w", err)
	}

	return s.watchExecution(ctx, task, exitCh, stdoutLW, stderrLW, oleLimiter, req.Limits)
}

func cleanupResource(ctx context.Context, resource string, cleanup func(context.Context) error) {
	logCtx := context.WithoutCancel(ctx)
	cleanupCtx, cancel := context.WithTimeout(logCtx, lifecycleOperationTimeout)
	defer cancel()

	if err := cleanup(cleanupCtx); err != nil && !errdefs.IsNotFound(err) {
		slog.WarnContext(logCtx, "sandbox cleanup failed", "resource", resource, "error", err)
	}
}

func (*Sandbox) watchExecution(
	ctx context.Context,
	task taskController,
	exitCh <-chan containerd.ExitStatus,
	stdoutLW, stderrLW *limitedWriter,
	oleLimiter *outputLimiter,
	limits ResourceLimits,
) (ExecuteResult, error) {
	startTime := time.Now()
	if err := task.Start(ctx); err != nil {
		return ExecuteResult{}, fmt.Errorf("start task: %w", err)
	}
	if err := task.CloseIO(ctx, containerd.WithStdinCloser); err != nil && !errdefs.IsNotFound(err) {
		slog.DebugContext(context.WithoutCancel(ctx), "failed to close task stdin", "error", err)
	}

	wallDeadline := time.NewTimer(time.Duration(limits.WallTimeMs) * time.Millisecond)
	defer wallDeadline.Stop()
	cpuTicker := time.NewTicker(cpuTimeCheckInterval)
	defer cpuTicker.Stop()

	event := waitForExecutionEvent(ctx, task, exitCh, oleLimiter.ch, wallDeadline.C, cpuTicker.C, limits.CPUTimeMs)
	if event.exited {
		return resultAfterTaskExit(ctx, task, event.status, startTime, limits, stdoutLW, stderrLW)
	}

	if event.reason != cpuTimeLimitReason && event.err == nil {
		event.metrics, event.err = collectMetrics(ctx, task)
		if event.err != nil {
			event.err = fmt.Errorf("collect cgroup metrics: %w", event.err)
		}
	}
	stopErr := stopTask(ctx, task, exitCh, lifecycleOperationTimeout)
	if err := errors.Join(event.err, stopErr); err != nil {
		return ExecuteResult{}, err
	}

	return buildForcedStopVerdict(event.reason, event.metrics, limits, stdoutLW, stderrLW), nil
}

func waitForExecutionEvent(
	ctx context.Context,
	task metricsReader,
	exitCh <-chan containerd.ExitStatus,
	outputLimit <-chan struct{},
	wallDeadline, cpuTicks <-chan time.Time,
	cpuTimeLimitMs int,
) executionEvent {
	for {
		select {
		case status, ok := <-exitCh:
			if !ok {
				return executionEvent{err: errors.New("wait for task exit: exit channel closed without status")}
			}
			return executionEvent{status: status, exited: true}

		case <-cpuTicks:
			metrics, err := collectMetrics(ctx, task)
			if err != nil {
				return executionEvent{err: fmt.Errorf("monitor CPU time: %w", err)}
			}
			if metrics.cpuMillis() >= cpuTimeLimitMs {
				return executionEvent{reason: cpuTimeLimitReason, metrics: metrics}
			}

		case <-outputLimit:
			return executionEvent{reason: outputLimitReason}

		case <-wallDeadline:
			return executionEvent{reason: wallTimeLimitReason}

		case <-ctx.Done():
			return executionEvent{err: fmt.Errorf("execution canceled: %w", ctx.Err())}
		}
	}
}

func resultAfterTaskExit(
	ctx context.Context,
	task metricsReader,
	status containerd.ExitStatus,
	startTime time.Time,
	limits ResourceLimits,
	stdoutLW, stderrLW *limitedWriter,
) (ExecuteResult, error) {
	code, _, err := status.Result()
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("read task exit result: %w", err)
	}
	metrics, err := collectMetrics(ctx, task)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("collect cgroup metrics: %w", err)
	}
	return buildVerdict(code, time.Since(startTime), metrics, limits, stdoutLW, stderrLW), nil
}

func stopTask(
	ctx context.Context,
	task taskController,
	exitCh <-chan containerd.ExitStatus,
	timeout time.Duration,
) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	if err := task.Kill(stopCtx, syscall.SIGKILL, containerd.WithKillAll); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("kill task: %w", err)
	}

	select {
	case status, ok := <-exitCh:
		if !ok {
			return errors.New("wait for task exit after kill: exit channel closed without status")
		}
		if _, _, err := status.Result(); err != nil {
			return fmt.Errorf("wait for task exit after kill: %w", err)
		}
		return nil
	case <-stopCtx.Done():
		return fmt.Errorf("wait for task exit after kill: %w", stopCtx.Err())
	}
}

func generateContainerID() string {
	return fmt.Sprintf("sandbox-%016x", rand.Uint64())
}
