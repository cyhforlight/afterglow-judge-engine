package sandbox

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const bytesPerMiB = int64(1024 * 1024)

const pidsLimit = 128

// cpuCounter is used for round-robin CPU allocation across all sandbox instances.
var cpuCounter atomic.Uint32

func sandboxSpecOpts(req ExecuteRequest) ([]oci.SpecOpts, error) {
	opts, err := mountSpecOpts(req.MountDir)
	if err != nil {
		return nil, err
	}

	cwd, hasCwd, err := resolveCwd(req)
	if err != nil {
		return nil, err
	}
	if hasCwd {
		opts = append(opts, oci.WithProcessCwd(cwd))
	}

	memoryLimitBytes, err := memoryBytesFromMB(req.Limits.MemoryMB)
	if err != nil {
		return nil, err
	}

	opts = append(opts,
		oci.WithMemoryLimit(uint64(memoryLimitBytes)), //nolint:gosec // memoryBytesFromMB guarantees a positive int64 value.
		oci.WithMemorySwap(memoryLimitBytes),
		sandboxSecurityOpts(req.EnableSeccomp),
	)

	return opts, nil
}

func mountSpecOpts(mount *Mount) ([]oci.SpecOpts, error) {
	if mount == nil {
		return nil, nil
	}
	if mount.ContainerPath == "" {
		return nil, errors.New("mount dir container path is required")
	}
	if !filepath.IsAbs(mount.ContainerPath) {
		return nil, fmt.Errorf("mount dir container path must be absolute: %q", mount.ContainerPath)
	}

	opts := []string{"rbind"}
	if mount.ReadOnly {
		opts = append(opts, "ro")
	}

	return []oci.SpecOpts{oci.WithMounts([]specs.Mount{{
		Destination: mount.ContainerPath,
		Type:        "bind",
		Source:      mount.HostPath,
		Options:     opts,
	}})}, nil
}

func memoryBytesFromMB(memoryMB int) (int64, error) {
	if memoryMB <= 0 {
		return 0, errors.New("memory limit must be positive")
	}
	if memoryMB > math.MaxInt64/int(bytesPerMiB) {
		return 0, fmt.Errorf("memory limit too large: %dMB", memoryMB)
	}
	return int64(memoryMB) * bytesPerMiB, nil
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

// pickCPU selects a CPU core using round-robin allocation.
// This distributes load evenly across all available cores.
func pickCPU() string {
	cpuCount := runtime.NumCPU()
	if cpuCount <= 1 {
		return "0"
	}
	next := int(cpuCounter.Add(1))
	cpu := next % cpuCount
	return strconv.Itoa(cpu)
}

func sandboxSecurityOpts(enableSeccomp bool) oci.SpecOpts {
	opts := []oci.SpecOpts{
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
	}

	if enableSeccomp {
		opts = append(opts, withJudgeSandboxSeccomp())
	}

	return oci.Compose(opts...)
}

// withJudgeSandboxSeccomp applies seccomp restrictions for judge sandbox.
// It blocks network operations and process creation syscalls while allowing
// thread creation (clone) needed by JVM and Python interpreters.
func withJudgeSandboxSeccomp() oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
		if s.Linux == nil {
			s.Linux = &specs.Linux{}
		}

		blockedSyscalls := []string{
			"socket", "bind", "listen", "connect", "accept", "accept4",
			"sendto", "recvfrom", "sendmsg", "recvmsg",
			"fork", "vfork",
			"ptrace",
			"mount", "umount2",
			"reboot",
		}

		s.Linux.Seccomp = &specs.LinuxSeccomp{
			DefaultAction: specs.ActAllow,
			Architectures: []specs.Arch{specs.ArchX86_64, specs.ArchX86},
			Syscalls: []specs.LinuxSyscall{
				{
					Names:  blockedSyscalls,
					Action: specs.ActErrno,
				},
			},
		}
		return nil
	}
}
