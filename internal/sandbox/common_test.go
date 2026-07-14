package sandbox

import (
	"context"
	"testing"
	"time"
)

const (
	testPythonImageRef = "docker.io/library/python:3.11-slim-bookworm"
	testStaticImageRef = "docker.io/library/debian:12-slim"
)

func requireSandboxIntegrationTest(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	sb, err := New("", "")
	if err != nil {
		t.Skipf("sandbox integration environment unavailable: %v", err)
	}
	if err := sb.CheckReadiness(ctx); err != nil {
		t.Skipf("sandbox integration environment unavailable: %v", err)
	}
}

func newSandboxTestContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	return ctx
}

type sandboxTestEnv struct {
	sb  *Sandbox
	ctx context.Context
}

func newSandboxTestEnv(t *testing.T) sandboxTestEnv {
	t.Helper()
	requireSandboxIntegrationTest(t)

	sb, err := New("", "")
	if err != nil {
		t.Fatalf("initialize sandbox: %v", err)
	}

	return sandboxTestEnv{
		sb:  sb,
		ctx: newSandboxTestContext(t, 10*time.Second),
	}
}

func standardLimits() ResourceLimits {
	return ResourceLimits{
		CPUTimeMs:   1000,
		WallTimeMs:  3000,
		MemoryMB:    128,
		OutputBytes: 1024,
	}
}

func tightLimits(cpuMs, memMB int) ResourceLimits {
	return ResourceLimits{
		CPUTimeMs:   cpuMs,
		WallTimeMs:  cpuMs * 3,
		MemoryMB:    memMB,
		OutputBytes: 1024,
	}
}
