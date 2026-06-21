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
	defaultSocketPath = "/run/containerd/containerd.sock"
	defaultNamespace  = "afterglow"
)

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
	var cleanups []func()
	succeeded := false

	addCleanup := func(fn func()) { cleanups = append(cleanups, fn) }
	rollback := func() {
		for _, cleanup := range slices.Backward(cleanups) {
			cleanup()
		}
	}
	defer func() {
		if !succeeded {
			rollback()
		}
	}()

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
	addCleanup(func() { _ = container.Delete(ctx, containerd.WithSnapshotCleanup) })

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
	addCleanup(func() { _, _ = task.Delete(ctx) })

	exitCh, err := task.Wait(ctx)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("setup wait: %w", err)
	}

	result, err := s.watchExecution(ctx, task, exitCh, stdoutLW, stderrLW, oleLimiter, req.Limits)
	if err != nil {
		return ExecuteResult{}, err
	}

	succeeded = true
	rollback()
	return result, nil
}

func (*ContainerdSandbox) watchExecution(
	ctx context.Context,
	task containerd.Task,
	exitCh <-chan containerd.ExitStatus,
	stdoutLW, stderrLW *limitedWriter,
	oleLimiter *outputLimiter,
	limits ResourceLimits,
) (ExecuteResult, error) {
	startTime := time.Now()
	if err := task.Start(ctx); err != nil {
		return ExecuteResult{}, fmt.Errorf("start task: %w", err)
	}
	_ = task.CloseIO(ctx, containerd.WithStdinCloser)

	wallDeadline := time.NewTimer(time.Duration(limits.WallTimeMs) * time.Millisecond)
	defer wallDeadline.Stop()

	var forcedStopReason string
	select {
	case status := <-exitCh:
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
	}

	metrics := collectMetrics(ctx, task)
	_ = task.Kill(ctx, syscall.SIGKILL)
	<-exitCh

	return buildForcedStopVerdict(forcedStopReason, metrics, limits, stdoutLW, stderrLW), nil
}

func generateContainerID() string {
	return fmt.Sprintf("sandbox-%016x", rand.Uint64())
}
