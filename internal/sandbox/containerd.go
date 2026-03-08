package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	cgroupsv2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	typeurl "github.com/containerd/typeurl/v2"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	defaultSocketPath          = "/run/containerd/containerd.sock"
	defaultNamespace           = "afterglow"
	cgroupV2CheckPath          = "/sys/fs/cgroup/cgroup.controllers"
	bytesPerMiB                = int64(1024 * 1024)
	nanosPerMs                 = uint64(1_000_000)
	pidsLimit                  = 128
	memoryHitThresholdPermille = 995
)

// ContainerdSandbox implements Sandbox using containerd.
type ContainerdSandbox struct {
	socketPath string
	namespace  string
	log        *slog.Logger

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
		log:             slog.Default(),
		checkCgroupV2:   ensureCgroupV2Enabled,
		checkContainerd: ensureContainerdAvailable,
	}
}

func ensureCgroupV2Enabled() error {
	_, err := os.Stat(cgroupV2CheckPath)
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("cgroup v2 is required: missing /sys/fs/cgroup/cgroup.controllers")
	}
	return fmt.Errorf("check cgroup v2 mount: %w", err)
}

func ensureContainerdAvailable(ctx context.Context, socketPath string) error {
	client, err := containerd.New(socketPath)
	if err != nil {
		return fmt.Errorf("connect to containerd socket %q: %w", socketPath, err)
	}
	defer func() { _ = client.Close() }()

	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if _, err := client.Version(checkCtx); err != nil {
		return fmt.Errorf("ping containerd on %q: %w", socketPath, err)
	}
	return nil
}

// PreflightCheck verifies that cgroup v2 and containerd are available.
func (s *ContainerdSandbox) PreflightCheck(ctx context.Context) error {
	if err := s.checkCgroupV2(); err != nil {
		return err
	}
	if err := s.checkContainerd(ctx, s.socketPath); err != nil {
		return err
	}
	s.log.DebugContext(ctx, "sandbox preflight checks passed")
	return nil
}

// Execute runs a command in an isolated container.
func (s *ContainerdSandbox) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
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

func (s *ContainerdSandbox) ensureImage(ctx context.Context, client *containerd.Client, imageRef string) (containerd.Image, error) {
	image, err := client.GetImage(ctx, imageRef)
	if err == nil {
		s.log.DebugContext(ctx, "image found locally", "ref", imageRef)
		return image, nil
	}
	if !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("get image %q: %w", imageRef, err)
	}
	s.log.InfoContext(ctx, "pulling image", "ref", imageRef)
	image, err = client.Pull(ctx, imageRef, containerd.WithPullUnpack)
	if err != nil {
		return nil, fmt.Errorf("pull image %q: %w", imageRef, err)
	}
	return image, nil
}

//nolint:funlen // Container setup requires sequential resource allocation
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
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}
	defer func() {
		if !succeeded {
			rollback()
		}
	}()

	containerID := generateContainerID()
	specOpts := []oci.SpecOpts{
		oci.WithImageConfig(image),
		oci.WithProcessArgs(req.Command...),
	}

	if req.MountDir != nil {
		opts := []string{"rbind"}
		if req.MountDir.ReadOnly {
			opts = append(opts, "ro")
		}
		specOpts = append(specOpts, oci.WithMounts([]specs.Mount{{
			Destination: req.MountDir.ContainerPath,
			Type:        "bind",
			Source:      req.MountDir.HostPath,
			Options:     opts,
		}}))
	}

	cwd, hasCwd, err := resolveCwd(req)
	if err != nil {
		return ExecuteResult{}, err
	}
	if hasCwd {
		specOpts = append(specOpts, oci.WithProcessCwd(cwd))
	}

	memoryLimitBytes := int64(req.Limits.MemoryMB) * bytesPerMiB
	specOpts = append(specOpts,
		oci.WithMemoryLimit(uint64(memoryLimitBytes)), //nolint:gosec // G115: value is validated
		oci.WithMemorySwap(memoryLimitBytes),
		sandboxSecurityOpts(),
	)

	container, err := client.NewContainer(ctx, containerID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(containerID+"-snap", image),
		containerd.WithNewSpec(specOpts...),
	)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("create container: %w", err)
	}
	addCleanup(func() { _ = container.Delete(ctx, containerd.WithSnapshotCleanup) })

	s.log.DebugContext(ctx, "container created", "id", containerID, "image", req.ImageRef)

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

func resolveCwd(req ExecuteRequest) (string, bool, error) {
	if req.Cwd != nil {
		if !filepath.IsAbs(*req.Cwd) {
			return "", false, fmt.Errorf("cwd must be an absolute path: %q", *req.Cwd)
		}
		return *req.Cwd, true, nil
	}

	if req.MountDir != nil {
		if req.MountDir.ContainerPath == "" {
			return "", false, errors.New("mount dir container path is required")
		}
		if !filepath.IsAbs(req.MountDir.ContainerPath) {
			return "", false, fmt.Errorf("mount dir container path must be absolute: %q", req.MountDir.ContainerPath)
		}
		return req.MountDir.ContainerPath, true, nil
	}

	return "", false, nil
}

func (s *ContainerdSandbox) watchExecution(
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
	return fmt.Sprintf("sandbox-%016x", rand.Uint64()) //nolint:gosec // G404: math/rand/v2 is cryptographically seeded
}

func pickCPU() string {
	cpuCount := runtime.NumCPU()
	if cpuCount <= 1 {
		return "0"
	}
	return strconv.Itoa(rand.IntN(cpuCount)) //nolint:gosec // G404: math/rand/v2 is cryptographically seeded
}

func sandboxSecurityOpts() oci.SpecOpts {
	return oci.Compose(
		oci.WithRootFSReadonly(),
		oci.WithMounts([]specs.Mount{{
			Destination: "/tmp",
			Type:        "tmpfs",
			Source:      "tmpfs",
			Options:     []string{"nosuid", "nodev"},
		}}),
		oci.WithCPUs(pickCPU()),
		oci.WithCapabilities([]string{}),
		oci.WithNoNewPrivileges,
		oci.WithPidsLimit(pidsLimit),
	)
}

type cgroupMetrics struct {
	cpuNanos        uint64
	peakMemBytes    uint64
	memoryLimitHit  bool
	oomKillDetected bool
}

func (m cgroupMetrics) cpuMillis() int {
	return int(m.cpuNanos / nanosPerMs) //nolint:gosec // G115: value range is validated by cgroup limits
}

func (m cgroupMetrics) peakMemMB() int {
	return int(m.peakMemBytes / uint64(bytesPerMiB)) //nolint:gosec // G115: value range is validated by cgroup limits
}

func collectMetrics(ctx context.Context, task containerd.Task) cgroupMetrics {
	metric, err := task.Metrics(ctx)
	if err != nil {
		return cgroupMetrics{}
	}
	return parseCgroupMetrics(metric.Data)
}

func parseCgroupMetrics(data typeurl.Any) cgroupMetrics {
	var m cgroupMetrics

	var v2 cgroupsv2.Metrics
	if err := typeurl.UnmarshalTo(data, &v2); err != nil {
		return m
	}

	if v2.CPU != nil {
		m.cpuNanos = v2.CPU.UsageUsec * 1000
	}
	if v2.Memory != nil {
		m.peakMemBytes = max(v2.Memory.MaxUsage, v2.Memory.Usage)
	}
	if v2.MemoryEvents != nil {
		if v2.MemoryEvents.OomKill > 0 {
			m.oomKillDetected = true
		}
		if v2.MemoryEvents.Max > 0 || v2.MemoryEvents.Oom > 0 {
			m.memoryLimitHit = true
		}
	}
	return m
}

func buildVerdict(
	exitCode uint32,
	wallElapsed time.Duration,
	metrics cgroupMetrics,
	limits ResourceLimits,
	stdoutLW, stderrLW *limitedWriter,
) ExecuteResult {
	cpuMs := metrics.cpuMillis()
	peakMemMB := metrics.peakMemMB()

	res := ExecuteResult{
		ExitCode:  int(exitCode),
		Stdout:    stdoutLW.String(),
		Stderr:    stderrLW.String(),
		CPUTimeMs: cpuMs,
		MemoryMB:  peakMemMB,
	}

	if cpuMs == 0 {
		res.CPUTimeMs = int(wallElapsed.Milliseconds())
	}

	memoryHitLimit := memoryLimitReached(metrics, limits.MemoryMB)

	switch {
	case outputOverflowed(stdoutLW, stderrLW):
		res.Verdict = VerdictOLE
		res.ExtraInfo = fmt.Sprintf("output limit exceeded (%d bytes max)", limits.OutputBytes)

	case metrics.oomKillDetected || exitCode == 137 || (exitCode != 0 && memoryHitLimit):
		res.Verdict = VerdictMLE
		res.ExtraInfo = fmt.Sprintf("memory limit exceeded (peak %dMB, limit %dMB)", peakMemMB, limits.MemoryMB)

	case cpuMs > limits.CPUTimeMs:
		res.Verdict = VerdictTLE
		res.ExtraInfo = fmt.Sprintf("CPU time exceeded: %dms > %dms", cpuMs, limits.CPUTimeMs)

	case exitCode == 0:
		res.Verdict = VerdictOK

	default:
		res.Verdict = VerdictRE
		res.ExtraInfo = stderrLW.String()
	}
	return res
}

func buildForcedStopVerdict(
	reason string,
	metrics cgroupMetrics,
	limits ResourceLimits,
	stdoutLW, stderrLW *limitedWriter,
) ExecuteResult {
	cpuMs := min(metrics.cpuMillis(), limits.CPUTimeMs)
	peakMemMB := metrics.peakMemMB()

	res := ExecuteResult{
		CPUTimeMs: cpuMs,
		MemoryMB:  peakMemMB,
		Stdout:    stdoutLW.String(),
		Stderr:    stderrLW.String(),
	}

	if outputOverflowed(stdoutLW, stderrLW) {
		res.Verdict = VerdictOLE
		res.ExtraInfo = fmt.Sprintf("output limit exceeded (%d bytes max)", limits.OutputBytes)
		return res
	}
	if metrics.oomKillDetected || memoryLimitReached(metrics, limits.MemoryMB) {
		res.Verdict = VerdictMLE
		res.ExtraInfo = fmt.Sprintf("memory limit exceeded (peak %dMB, limit %dMB)", peakMemMB, limits.MemoryMB)
		return res
	}
	res.Verdict = VerdictTLE
	res.ExtraInfo = fmt.Sprintf("%s (%dms wall, cpu limit %dms)", reason, limits.WallTimeMs, limits.CPUTimeMs)
	return res
}

func memoryLimitReached(metrics cgroupMetrics, memoryLimitMB int) bool {
	if metrics.memoryLimitHit {
		return true
	}
	if memoryLimitMB <= 0 {
		return false
	}

	limitBytes := uint64(memoryLimitMB) * uint64(bytesPerMiB)
	if metrics.peakMemBytes >= limitBytes {
		return true
	}
	return metrics.peakMemBytes*1000 >= limitBytes*memoryHitThresholdPermille
}

func outputOverflowed(stdoutLW, stderrLW *limitedWriter) bool {
	return stdoutLW.isOverflowed() || stderrLW.isOverflowed()
}

// outputLimiter holds a shared byte budget for all associated limitedWriters.
type outputLimiter struct {
	ch    chan struct{}
	once  sync.Once
	mu    sync.Mutex
	limit int64
	used  int64
}

func newOutputLimiter(maxBytes int64) *outputLimiter {
	return &outputLimiter{ch: make(chan struct{}), limit: maxBytes}
}

func (l *outputLimiter) signal() {
	l.once.Do(func() { close(l.ch) })
}

func (l *outputLimiter) reserve(requested int64) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	remaining := l.limit - l.used
	if remaining <= 0 {
		return 0
	}
	granted := min(requested, remaining)
	l.used += granted
	return granted
}

// limitedWriter buffers output while drawing bytes from the shared outputLimiter pool.
type limitedWriter struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	overflowed bool
	limiter    *outputLimiter
}

func newLimitedWriter(limiter *outputLimiter) *limitedWriter {
	return &limitedWriter{limiter: limiter}
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(p)
	if w.overflowed {
		return n, nil
	}

	allowed := w.limiter.reserve(int64(n))
	if allowed > 0 {
		_, _ = w.buf.Write(p[:allowed])
	}
	if allowed < int64(n) {
		w.overflowed = true
		w.limiter.signal()
	}
	return n, nil
}

func (w *limitedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *limitedWriter) isOverflowed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.overflowed
}
