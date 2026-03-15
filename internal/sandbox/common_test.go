package sandbox

import (
	"context"
	"testing"
	"time"
)

const (
	testPythonImageRef = "gcr.io/distroless/python3-debian12:latest"
	testStaticImageRef = "gcr.io/distroless/static-debian12:latest"
)

func requireSandboxIntegrationTest(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sb := NewContainerdSandbox("", "")
	if err := sb.PreflightCheck(ctx); err != nil {
		t.Skipf("sandbox integration environment unavailable: %v", err)
	}
}

func newSandboxTestContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func newTestSandbox(t *testing.T) *ContainerdSandbox {
	t.Helper()

	return NewContainerdSandbox("", "")
}

// sandboxTestEnv encapsulates common test dependencies
type sandboxTestEnv struct {
	sb  *ContainerdSandbox
	ctx context.Context
}

// newSandboxTestEnv creates a complete test environment with custom timeout
func newSandboxTestEnv(t *testing.T, timeout time.Duration) sandboxTestEnv {
	t.Helper()
	requireSandboxIntegrationTest(t)

	return sandboxTestEnv{
		sb:  newTestSandbox(t),
		ctx: newSandboxTestContext(t, timeout),
	}
}

// newStandardSandboxTestEnv creates test environment with 10s timeout (most common)
func newStandardSandboxTestEnv(t *testing.T) sandboxTestEnv {
	t.Helper()
	return newSandboxTestEnv(t, 10*time.Second)
}

// standardLimits returns the most common ResourceLimits configuration
func standardLimits() ResourceLimits {
	return ResourceLimits{
		CPUTimeMs:   1000,
		WallTimeMs:  3000,
		MemoryMB:    128,
		OutputBytes: 1024,
	}
}

// tightLimits returns limits for testing TLE/MLE scenarios
func tightLimits(cpuMs, memMB int) ResourceLimits {
	return ResourceLimits{
		CPUTimeMs:   cpuMs,
		WallTimeMs:  cpuMs * 3,
		MemoryMB:    memMB,
		OutputBytes: 1024,
	}
}

// largeLimits returns limits for tests needing more resources
func largeLimits() ResourceLimits {
	return ResourceLimits{
		CPUTimeMs:   5000,
		WallTimeMs:  15000,
		MemoryMB:    128,
		OutputBytes: 1024 * 1024, // 1MB
	}
}
