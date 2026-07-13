package sandbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/cgroups/v3"
	containerd "github.com/containerd/containerd/v2/client"
)

func ensureCgroupV2Enabled() error {
	if cgroups.Mode() != cgroups.Unified {
		return errors.New("cgroup v2 unified mode is required")
	}
	return nil
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
