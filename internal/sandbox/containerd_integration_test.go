package sandbox

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const multipleMountsProgram = `
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

// TestContainerdSandbox_Integration tests require containerd to be running.
// Run with: go test -tags=integration ./internal/sandbox/...

func TestContainerdSandbox_PreflightCheck(t *testing.T) {
	requireSandboxIntegrationTest(t)

	sb := newTestSandbox(t)
	ctx := newSandboxTestContext(t, 5*time.Second)

	err := sb.PreflightCheck(ctx)
	assert.NoError(t, err, "Preflight check should pass when containerd is running")
}

func TestContainerdSandbox_Execute_SimpleEcho(t *testing.T) {
	requireSandboxIntegrationTest(t)

	sb := newTestSandbox(t)
	ctx := newSandboxTestContext(t, 30*time.Second)

	tmpDir := t.TempDir()
	writeTempTestFile(t, tmpDir, "hello.c", `
#include <stdio.h>
int main() {
    printf("Hello World\n");
    return 0;
}
`, 0o644)
	compileTempProgram(ctx, t, sb, tmpDir, "hello.c", "hello")

	req := ExecuteRequest{
		ImageRef: testRuntimeImageRef,
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

func TestContainerdSandbox_Execute_WithStdin(t *testing.T) {
	requireSandboxIntegrationTest(t)

	sb := newTestSandbox(t)
	ctx := newSandboxTestContext(t, 60*time.Second)

	tmpDir := t.TempDir()
	writeTempTestFile(t, tmpDir, "echo.c", `
#include <stdio.h>
int main() {
    char buffer[1024];
    if (fgets(buffer, sizeof(buffer), stdin)) {
        printf("%s", buffer);
    }
    return 0;
}
`, 0o644)
	compileTempProgram(ctx, t, sb, tmpDir, "echo.c", "echo")

	stdin := bytes.NewBufferString("test input\n")

	req := ExecuteRequest{
		ImageRef: testRuntimeImageRef,
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
	requireSandboxIntegrationTest(t)

	sb := newTestSandbox(t)
	ctx := newSandboxTestContext(t, 30*time.Second)

	tmpDir := t.TempDir()
	writeTempTestFile(t, tmpDir, "loop.sh", "#!/bin/sh\nwhile true; do :; done\n", 0o755)

	req := ExecuteRequest{
		ImageRef: testScriptImageRef,
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
	requireSandboxIntegrationTest(t)

	sb := newTestSandbox(t)
	ctx := newSandboxTestContext(t, 30*time.Second)

	tmpDir := t.TempDir()
	writeTempTestFile(t, tmpDir, "output.sh", "#!/bin/sh\nyes | head -c 2000000\n", 0o755)

	req := ExecuteRequest{
		ImageRef: testScriptImageRef,
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
	requireSandboxIntegrationTest(t)

	sb := newTestSandbox(t)
	ctx := newSandboxTestContext(t, 30*time.Second)

	tmpDir := t.TempDir()
	writeTempTestFile(t, tmpDir, "fail.sh", "#!/bin/sh\nexit 42\n", 0o755)

	req := ExecuteRequest{
		ImageRef: testScriptImageRef,
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

func TestContainerdSandbox_Execute_MultipleMounts(t *testing.T) {
	requireSandboxIntegrationTest(t)

	sb := newTestSandbox(t)
	ctx := newSandboxTestContext(t, 30*time.Second)

	tmpDir1 := t.TempDir()
	tmpDir2 := t.TempDir()

	writeTempTestFile(t, tmpDir1, "file1.txt", "content1", 0o644)
	writeTempTestFile(t, tmpDir2, "file2.txt", "content2", 0o644)

	compileDir := t.TempDir()
	writeTempTestFile(t, compileDir, "test.c", multipleMountsProgram, 0o644)
	compileTempProgram(ctx, t, sb, compileDir, "test.c", "test")

	req := ExecuteRequest{
		ImageRef: testRuntimeImageRef,
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
