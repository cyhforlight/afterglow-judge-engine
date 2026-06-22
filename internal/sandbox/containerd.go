package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
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
)

type taskController interface {
	metricsReader
	Start(context.Context) error
	CloseIO(context.Context, ...containerd.IOCloserOpts) error
	Kill(context.Context, syscall.Signal, ...containerd.KillOpts) error
}

type cleanupAction struct {
	resource string
	run      func(context.Context) error
}

// ContainerdSandbox implements Sandbox using containerd.
type ContainerdSandbox struct {
	socketPath string
	namespace  string

	checkCgroupV2   func() error
	checkContainerd func(ctx context.Context, socketPath string) error
}

// NewContainerdSandbox creates a new containerd-based sandbox.
func NewContainerdSandbox(socketPath, namespace string) *ContainerdSandbox {
	if socketPath == "" {
		socketPath = defaultSocketPath
	}
	if namespace == "" {
		namespace = defaultNamespace
	}

	return &ContainerdSandbox{
		socketPath:      socketPath,
		namespace:       namespace,
		checkCgroupV2:   ensureCgroupV2Enabled,
		checkContainerd: ensureContainerdAvailable,
	}
}

// PreflightCheck verifies that cgroup v2 and containerd are available.
func (s *ContainerdSandbox) PreflightCheck(ctx context.Context) error {
	if err := s.checkCgroupV2(); err != nil {
		return err
	}
	if err := s.checkContainerd(ctx, s.socketPath); err != nil {
		return err
	}
	slog.DebugContext(ctx, "sandbox preflight checks passed")
	return nil
}

// Execute runs a command in an isolated container.
func (s *ContainerdSandbox) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
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

func (*ContainerdSandbox) ensureImage(ctx context.Context, client *containerd.Client, imageRef string) (containerd.Image, error) {
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

func (s *ContainerdSandbox) executeInContainer(
	ctx context.Context,
	client *containerd.Client,
	image containerd.Image,
	req ExecuteRequest,
) (ExecuteResult, error) {
	var cleanups []cleanupAction
	defer func() { runCleanupActions(ctx, cleanups) }()

	containerID := generateContainerID()
	requestSpecOpts, err := sandboxSpecOpts(req)
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
	cleanups = append(cleanups, cleanupAction{
		resource: "container and snapshot",
		run: func(cleanupCtx context.Context) error {
			return container.Delete(cleanupCtx, containerd.WithSnapshotCleanup)
		},
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
	cleanups = append(cleanups, cleanupAction{
		resource: "task",
		run: func(cleanupCtx context.Context) error {
			_, err := task.Delete(cleanupCtx, containerd.WithProcessKill)
			return err
		},
	})

	waitCtx, cancelWait := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWait()

	exitCh, err := task.Wait(waitCtx)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("setup wait: %w", err)
	}

	return s.watchExecution(ctx, task, exitCh, stdoutLW, stderrLW, oleLimiter, req.Limits)
}

func runCleanupActions(ctx context.Context, actions []cleanupAction) {
	logCtx := context.WithoutCancel(ctx)
	for _, action := range slices.Backward(actions) {
		cleanupCtx, cancel := context.WithTimeout(logCtx, lifecycleOperationTimeout)
		err := action.run(cleanupCtx)
		cancel()
		if err != nil && !errdefs.IsNotFound(err) {
			slog.WarnContext(logCtx, "sandbox cleanup failed", "resource", action.resource, "error", err)
		}
	}
}

func (*ContainerdSandbox) watchExecution(
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

	var forcedStopReason string
	select {
	case status, ok := <-exitCh:
		if !ok {
			return ExecuteResult{}, errors.New("wait for task exit: exit channel closed without status")
		}
		wallElapsed := time.Since(startTime)
		code, _, err := status.Result()
		if err != nil {
			return ExecuteResult{}, fmt.Errorf("read task exit result: %w", err)
		}
		metrics := collectMetrics(ctx, task)
		return buildVerdict(code, wallElapsed, metrics, limits, stdoutLW, stderrLW), nil

	case <-oleLimiter.ch:
		forcedStopReason = "output limit exceeded"

	case <-wallDeadline.C:
		forcedStopReason = "wall time limit exceeded"

	case <-ctx.Done():
		cancelErr := ctx.Err()
		if err := stopTask(ctx, task, exitCh, lifecycleOperationTimeout); err != nil {
			return ExecuteResult{}, errors.Join(
				fmt.Errorf("execution canceled: %w", cancelErr),
				err,
			)
		}
		return ExecuteResult{}, fmt.Errorf("execution canceled: %w", cancelErr)
	}

	metrics := collectMetrics(ctx, task)
	if err := stopTask(ctx, task, exitCh, lifecycleOperationTimeout); err != nil {
		return ExecuteResult{}, err
	}

	return buildForcedStopVerdict(forcedStopReason, metrics, limits, stdoutLW, stderrLW), nil
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
