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
