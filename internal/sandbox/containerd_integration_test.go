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

func TestContainerdSandbox_Cancellation(t *testing.T) {
	requireSandboxIntegrationTest(t)

	sb, err := NewContainerdSandbox("", "")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Ensure image setup is not part of the cancellation window under test.
	warmupCtx := newSandboxTestContext(t, 30*time.Second)
	_, err = sb.Execute(warmupCtx, ExecuteRequest{
		ImageRef: testPythonImageRef,
		Command:  []string{"python3", "-c", "pass"},
		Limits:   standardLimits(),
	})
	require.NoError(t, err)

	cancelAfter := 200 * time.Millisecond
	cancelTimer := time.AfterFunc(cancelAfter, cancel)
	defer cancelTimer.Stop()

	startedAt := time.Now()
	_, err = sb.Execute(ctx, ExecuteRequest{
		ImageRef: testPythonImageRef,
		Command:  []string{"python3", "-c", "while True: pass"},
		Limits: ResourceLimits{
			CPUTimeMs:   30_000,
			WallTimeMs:  30_000,
			MemoryMB:    128,
			OutputBytes: 1024,
		},
	})

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(startedAt), 5*time.Second)
}

func TestContainerdSandbox_PinsExecutionToOneCPU(t *testing.T) {
	env := newSandboxTestEnv(t)

	result, err := env.sb.Execute(env.ctx, ExecuteRequest{
		ImageRef: testPythonImageRef,
		Command:  []string{"python3", "-c", "import os; print(len(os.sched_getaffinity(0)))"},
		Limits:   standardLimits(),
	})
	require.NoError(t, err)
	assert.Equal(t, VerdictOK, result.Verdict)
	assert.Equal(t, "1\n", result.Stdout)
}

func TestContainerdSandbox_VerdictScenarios(t *testing.T) {
	tests := []struct {
		name            string
		script          string
		limits          ResourceLimits
		expectedCode    int
		expectedVerdict Verdict
		checkExtraInfo  string // Substring to check in ExtraInfo
		minCPUTimeMs    int
	}{
		{
			name:            "OK - simple output",
			script:          "print('Hello World')",
			limits:          standardLimits(),
			expectedCode:    0,
			expectedVerdict: VerdictOK,
		},
		{
			name:            "TLE - infinite loop",
			script:          "while True: pass",
			limits:          tightLimits(100, 128),
			expectedCode:    0,
			expectedVerdict: VerdictTLE,
			checkExtraInfo:  "CPU time limit exceeded",
			minCPUTimeMs:    100,
		},
		{
			name:            "TLE - wall watchdog",
			script:          "import time; time.sleep(1)",
			limits:          tightLimits(100, 128),
			expectedCode:    0,
			expectedVerdict: VerdictTLE,
			checkExtraInfo:  "wall time limit exceeded",
		},
		{
			name:            "MLE - large allocation",
			script:          "x = bytearray(100 * 1024 * 1024)",
			limits:          tightLimits(5000, 64),
			expectedCode:    -1, // Don't assert specific exit code (can be 137, or other non-zero)
			expectedVerdict: VerdictMLE,
			checkExtraInfo:  "memory limit",
		},
		{
			name:   "OLE - excessive output",
			script: "print('x' * 10000000)",
			limits: ResourceLimits{
				CPUTimeMs:   5000,
				WallTimeMs:  15000,
				MemoryMB:    128,
				OutputBytes: 1024 * 1024,
			},
			expectedCode:    0,
			expectedVerdict: VerdictOLE,
			checkExtraInfo:  "output limit",
		},
		{
			name:            "RE - non-zero exit",
			script:          "import sys; sys.exit(42)",
			limits:          standardLimits(),
			expectedCode:    42,
			expectedVerdict: VerdictRE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newSandboxTestEnv(t)

			req := ExecuteRequest{
				ImageRef: testPythonImageRef,
				Command:  []string{"python3", "-c", tt.script},
				Limits:   tt.limits,
			}

			result, err := env.sb.Execute(env.ctx, req)
			require.NoError(t, err)

			if tt.expectedCode >= 0 {
				assert.Equal(t, tt.expectedCode, result.ExitCode)
			}
			assert.Equal(t, tt.expectedVerdict, result.Verdict)

			if tt.checkExtraInfo != "" {
				assert.Contains(t, result.ExtraInfo, tt.checkExtraInfo)
			}
			if tt.minCPUTimeMs > 0 {
				assert.GreaterOrEqual(t, result.CPUTimeMs, tt.minCPUTimeMs)
			}
		})
	}
}

func TestContainerdSandbox_IOOperations(t *testing.T) {
	tests := []struct {
		name        string
		setupMount  func(t *testing.T, tmpDir string)
		script      string
		stdin       string
		mountRO     bool
		checkResult func(t *testing.T, tmpDir string, result ExecuteResult)
	}{
		{
			name:   "stdin - read and process",
			script: "import sys; print(sys.stdin.read(), end='')",
			stdin:  "test input\n",
			checkResult: func(t *testing.T, _ string, result ExecuteResult) {
				assert.Contains(t, result.Stdout, "test input")
			},
		},
		{
			name: "read file - compute result",
			setupMount: func(t *testing.T, tmpDir string) {
				err := os.WriteFile(filepath.Join(tmpDir, "input.txt"), []byte("7\n"), 0o644)
				require.NoError(t, err)
			},
			script:  "n = int(open('/sandbox/input.txt').read()); print(n * n)",
			mountRO: true,
			checkResult: func(t *testing.T, _ string, result ExecuteResult) {
				assert.Contains(t, result.Stdout, "49")
			},
		},
		{
			name: "write file - create output",
			script: `
with open('/sandbox/result.txt', 'w') as f:
    f.write('test output')
print('done')
`,
			checkResult: func(t *testing.T, tmpDir string, _ ExecuteResult) {
				content, err := os.ReadFile(filepath.Join(tmpDir, "result.txt"))
				require.NoError(t, err)
				assert.Equal(t, "test output", string(content))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newSandboxTestEnv(t)
			tmpDir := t.TempDir()

			if tt.setupMount != nil {
				tt.setupMount(t, tmpDir)
			}

			req := ExecuteRequest{
				ImageRef: testPythonImageRef,
				Command:  []string{"python3", "-c", tt.script},
				Limits:   standardLimits(),
			}

			if tt.stdin != "" {
				req.Stdin = bytes.NewBufferString(tt.stdin)
			}

			req.MountDir = &Mount{
				HostPath:      tmpDir,
				ContainerPath: "/sandbox",
				ReadOnly:      tt.mountRO,
			}

			result, err := env.sb.Execute(env.ctx, req)
			require.NoError(t, err)
			assert.Equal(t, 0, result.ExitCode)
			assert.Equal(t, VerdictOK, result.Verdict)

			tt.checkResult(t, tmpDir, result)
		})
	}
}

func TestContainerdSandbox_SeccompEnforcement(t *testing.T) {
	env := newSandboxTestEnv(t)
	result, err := env.sb.Execute(env.ctx, ExecuteRequest{
		ImageRef: testPythonImageRef,
		Command: []string{"python3", "-c", `
import socket
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    print("socket succeeded")
except OSError as e:
    print(f"socket blocked: {e}")
`},
		Limits:        standardLimits(),
		EnableSeccomp: true,
	})
	require.NoError(t, err)
	assert.Contains(t, []Verdict{VerdictOK, VerdictRE}, result.Verdict)
	assert.Contains(t, result.Stdout, "socket blocked")
}
