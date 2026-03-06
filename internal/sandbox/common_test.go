package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	testCompileImageRef = "docker.io/library/gcc:12-bookworm"
	testRuntimeImageRef = "gcr.io/distroless/static-debian12:latest"
	testScriptImageRef  = "docker.io/library/alpine:latest"
)

func requireSandboxIntegrationTest(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
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

	return NewContainerdSandbox("")
}

func writeTempTestFile(t *testing.T, dir, name, content string, perm os.FileMode) {
	t.Helper()

	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), perm)
	require.NoError(t, err)
}

func compileTempProgram(
	ctx context.Context,
	t *testing.T,
	sb *ContainerdSandbox,
	dir, sourceName, outputName string,
) {
	t.Helper()

	result, err := sb.Execute(ctx, ExecuteRequest{
		ImageRef: testCompileImageRef,
		Command: []string{
			"gcc", "-static", "-o", filepath.Join("/work", outputName), filepath.Join("/work", sourceName),
		},
		Mounts: []Mount{{
			HostPath:      dir,
			ContainerPath: "/work",
			ReadOnly:      false,
		}},
		Limits: ResourceLimits{
			CPUTimeMs:   10000,
			WallTimeMs:  30000,
			MemoryMB:    512,
			OutputBytes: 1024 * 1024,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode, "Compilation failed: %s", result.Stderr)
}
