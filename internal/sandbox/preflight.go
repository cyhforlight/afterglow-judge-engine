package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
)

const cgroupV2CheckPath = "/sys/fs/cgroup/cgroup.controllers"

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
