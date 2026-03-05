package sandbox

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContainerdSandbox_Integration tests require containerd to be running.
// Run with: go test -tags=integration ./internal/sandbox/...

func TestContainerdSandbox_PreflightCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := NewContainerdSandbox("")
	ctx := context.Background()

	err := sb.PreflightCheck(ctx)
	assert.NoError(t, err, "Preflight check should pass when containerd is running")
}

//nolint:funlen // Integration test requires setup and verification
func TestContainerdSandbox_Execute_SimpleEcho(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := NewContainerdSandbox("")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a simple C program that prints Hello World
	tmpDir := t.TempDir()
	sourceFile := filepath.Join(tmpDir, "hello.c")
	err := os.WriteFile(sourceFile, []byte(`
#include <stdio.h>
int main() {
    printf("Hello World\n");
    return 0;
}
`), 0644)
	require.NoError(t, err)

	// Compile it first (we need gcc image for this)
	compileReq := ExecuteRequest{
		ImageRef: "docker.io/library/gcc:12-bookworm",
		Command:  []string{"gcc", "-static", "-o", "/work/hello", "/work/hello.c"},
		Mounts: []Mount{{
			HostPath:      tmpDir,
			ContainerPath: "/work",
			ReadOnly:      false,
		}},
		Limits: ResourceLimits{
			CPUTimeMs:   10000,
			WallTimeMs:  30000,
			MemoryMB:    512,
			OutputBytes: 1024 * 1024,
		},
	}

	compileResult, err := sb.Execute(ctx, compileReq)
	require.NoError(t, err)
	require.Equal(t, 0, compileResult.ExitCode, "Compilation should succeed")

	// Now execute the compiled binary
	req := ExecuteRequest{
		ImageRef: "gcr.io/distroless/static-debian12:latest",
		Command:  []string{"/sandbox/hello"},
		Mounts: []Mount{{
			HostPath:      tmpDir,
			ContainerPath: "/sandbox",
			ReadOnly:      true,
		}},
		Limits: ResourceLimits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: 1024,
		},
	}

	result, err := sb.Execute(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, VerdictOK, result.Verdict)
	assert.Contains(t, result.Stdout, "Hello World")
}

//nolint:funlen // Integration test requires setup and verification
func TestContainerdSandbox_Execute_WithStdin(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := NewContainerdSandbox("")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create a simple C program that reads and echoes input
	tmpDir := t.TempDir()
	sourceFile := filepath.Join(tmpDir, "echo.c")
	err := os.WriteFile(sourceFile, []byte(`
#include <stdio.h>
int main() {
    char buffer[1024];
    if (fgets(buffer, sizeof(buffer), stdin)) {
        printf("%s", buffer);
    }
    return 0;
}
`), 0644)
	require.NoError(t, err)

	// Compile
	compileReq := ExecuteRequest{
		ImageRef: "docker.io/library/gcc:12-bookworm",
		Command:  []string{"gcc", "-static", "-o", "/work/echo", "/work/echo.c"},
		Mounts: []Mount{{
			HostPath:      tmpDir,
			ContainerPath: "/work",
			ReadOnly:      false,
		}},
		Limits: ResourceLimits{
			CPUTimeMs:   10000,
			WallTimeMs:  30000,
			MemoryMB:    512,
			OutputBytes: 1024 * 1024,
		},
	}

	compileResult, err := sb.Execute(ctx, compileReq)
	require.NoError(t, err)
	require.Equal(t, 0, compileResult.ExitCode)

	// Execute with stdin
	stdin := bytes.NewBufferString("test input\n")

	req := ExecuteRequest{
		ImageRef: "gcr.io/distroless/static-debian12:latest",
		Command:  []string{"/sandbox/echo"},
		Mounts: []Mount{{
			HostPath:      tmpDir,
			ContainerPath: "/sandbox",
			ReadOnly:      true,
		}},
		Stdin: stdin,
		Limits: ResourceLimits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: 1024,
		},
	}

	result, err := sb.Execute(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, VerdictOK, result.Verdict)
	assert.Contains(t, result.Stdout, "test input")
}

func TestContainerdSandbox_Execute_TLE(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := NewContainerdSandbox("")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create an infinite loop script
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "loop.sh")
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nwhile true; do :; done\n"), 0755)
	require.NoError(t, err)

	req := ExecuteRequest{
		ImageRef: "docker.io/library/alpine:latest",
		Command:  []string{"/sandbox/loop.sh"},
		Mounts: []Mount{{
			HostPath:      tmpDir,
			ContainerPath: "/sandbox",
			ReadOnly:      true,
		}},
		Limits: ResourceLimits{
			CPUTimeMs:   100, // 100ms limit
			WallTimeMs:  300, // 300ms wall time
			MemoryMB:    128,
			OutputBytes: 1024,
		},
	}

	result, err := sb.Execute(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, VerdictTLE, result.Verdict)
	assert.Contains(t, result.ExtraInfo, "limit")
}

func TestContainerdSandbox_Execute_OLE(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := NewContainerdSandbox("")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a script that outputs a lot of data
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "output.sh")
	// Output 2MB of data (will exceed 1MB limit)
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nyes | head -c 2000000\n"), 0755)
	require.NoError(t, err)

	req := ExecuteRequest{
		ImageRef: "docker.io/library/alpine:latest",
		Command:  []string{"/sandbox/output.sh"},
		Mounts: []Mount{{
			HostPath:      tmpDir,
			ContainerPath: "/sandbox",
			ReadOnly:      true,
		}},
		Limits: ResourceLimits{
			CPUTimeMs:   5000,
			WallTimeMs:  15000,
			MemoryMB:    128,
			OutputBytes: 1024 * 1024, // 1MB limit
		},
	}

	result, err := sb.Execute(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, VerdictOLE, result.Verdict)
	assert.Contains(t, result.ExtraInfo, "output limit")
}

func TestContainerdSandbox_Execute_NonZeroExit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := NewContainerdSandbox("")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a script that exits with non-zero
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fail.sh")
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 42\n"), 0755)
	require.NoError(t, err)

	req := ExecuteRequest{
		ImageRef: "docker.io/library/alpine:latest",
		Command:  []string{"/sandbox/fail.sh"},
		Mounts: []Mount{{
			HostPath:      tmpDir,
			ContainerPath: "/sandbox",
			ReadOnly:      true,
		}},
		Limits: ResourceLimits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: 1024,
		},
	}

	result, err := sb.Execute(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 42, result.ExitCode)
	assert.Equal(t, VerdictRE, result.Verdict)
}

//nolint:funlen
func TestContainerdSandbox_Execute_MultipleMounts(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := NewContainerdSandbox("")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create two directories with different files
	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir1, "file1.txt"), []byte("content1"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tmpDir2, "file2.txt"), []byte("content2"), 0644)
	require.NoError(t, err)

	// Compile a C program that reads from both files
	sourceCode := `
#include <stdio.h>
int main() {
    FILE *f1 = fopen("/dir1/file1.txt", "r");
    FILE *f2 = fopen("/dir2/file2.txt", "r");
    char buf[100];
    if (f1) {
        fgets(buf, sizeof(buf), f1);
        printf("%s", buf);
        fclose(f1);
    }
    if (f2) {
        fgets(buf, sizeof(buf), f2);
        printf("%s", buf);
        fclose(f2);
    }
    return 0;
}
`
	compileDir := t.TempDir()
	srcPath := filepath.Join(compileDir, "test.c")
	err = os.WriteFile(srcPath, []byte(sourceCode), 0644)
	require.NoError(t, err)

	// Compile the program
	compileReq := ExecuteRequest{
		ImageRef: "docker.io/library/gcc:12-bookworm",
		Command:  []string{"gcc", "-static", "-o", "/work/test", "/work/test.c"},
		Mounts: []Mount{
			{
				HostPath:      compileDir,
				ContainerPath: "/work",
				ReadOnly:      false,
			},
		},
		Limits: ResourceLimits{
			CPUTimeMs:   10000,
			WallTimeMs:  30000,
			MemoryMB:    512,
			OutputBytes: 1024,
		},
	}
	compileResult, err := sb.Execute(ctx, compileReq)
	require.NoError(t, err)
	require.Equal(t, 0, compileResult.ExitCode, "Compilation failed: %s", compileResult.Stderr)

	// Execute the compiled program with multiple mounts
	req := ExecuteRequest{
		ImageRef: "gcr.io/distroless/static-debian12:latest",
		Command:  []string{"/program/test"},
		Mounts: []Mount{
			{
				HostPath:      compileDir,
				ContainerPath: "/program",
				ReadOnly:      true,
			},
			{
				HostPath:      tmpDir1,
				ContainerPath: "/dir1",
				ReadOnly:      true,
			},
			{
				HostPath:      tmpDir2,
				ContainerPath: "/dir2",
				ReadOnly:      true,
			},
		},
		Limits: ResourceLimits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: 1024,
		},
	}

	result, err := sb.Execute(ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, VerdictOK, result.Verdict)
	assert.Contains(t, result.Stdout, "content1")
	assert.Contains(t, result.Stdout, "content2")
}
