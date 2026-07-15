package sandbox

import (
	"context"
	"strconv"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const pidsLimit = 128

func sandboxSpecOpts(req ExecuteRequest, cpuID int) []oci.SpecOpts {
	opts := mountSpecOpts(req.MountDir)
	memoryLimitBytes := memoryBytes(req.Limits.MemoryMB)

	opts = append(opts,
		oci.WithMemoryLimit(uint64(memoryLimitBytes)), //nolint:gosec // Limits come from admitted requests or fixed profiles.
		oci.WithMemorySwap(memoryLimitBytes),
		sandboxSecurityOpts(req.EnableSeccomp, cpuID),
	)

	return opts
}

func mountSpecOpts(mount *Mount) []oci.SpecOpts {
	if mount == nil {
		return nil
	}

	opts := []string{"rbind"}
	if mount.ReadOnly {
		opts = append(opts, "ro")
	}

	return []oci.SpecOpts{
		oci.WithMounts([]specs.Mount{{
			Destination: mount.ContainerPath,
			Type:        "bind",
			Source:      mount.HostPath,
			Options:     opts,
		}}),
		oci.WithProcessCwd(mount.ContainerPath),
	}
}

func sandboxSecurityOpts(enableSeccomp bool, cpuID int) oci.SpecOpts {
	opts := []oci.SpecOpts{
		oci.WithRootFSReadonly(),
		oci.WithMounts([]specs.Mount{{
			Destination: "/tmp",
			Type:        "tmpfs",
			Source:      "tmpfs",
			Options:     []string{"nosuid", "nodev"},
		}}),
		oci.WithCPUs(strconv.Itoa(cpuID)),
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
