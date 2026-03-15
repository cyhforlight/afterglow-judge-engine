package sandbox

import (
	"bytes"
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
	requireSandboxIntegrationTest(t)

	sb := newTestSandbox(t)
	ctx := newSandboxTestContext(t, 5*time.Second)

	err := sb.PreflightCheck(ctx)
	assert.NoError(t, err, "Preflight check should pass when containerd is running")
}

func TestContainerdSandbox_Execute_SimpleEcho(t *testing.T) {
	env := newStandardSandboxTestEnv(t)

	req := ExecuteRequest{
		ImageRef: testPythonImageRef,
		Command:  []string{"python3", "-c", "print('Hello World')"},
		Limits:   standardLimits(),
	}

	result, err := env.sb.Execute(env.ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, VerdictOK, result.Verdict)
	assert.Contains(t, result.Stdout, "Hello World")
}

func TestContainerdSandbox_Execute_WithStdin(t *testing.T) {
	env := newStandardSandboxTestEnv(t)

	stdin := bytes.NewBufferString("test input\n")

	req := ExecuteRequest{
		ImageRef: testPythonImageRef,
		Command:  []string{"python3", "-c", "import sys; print(sys.stdin.read(), end='')"},
		Stdin:    stdin,
		Limits:   standardLimits(),
	}

	result, err := env.sb.Execute(env.ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, VerdictOK, result.Verdict)
	assert.Contains(t, result.Stdout, "test input")
}

func TestContainerdSandbox_VerdictScenarios(t *testing.T) {
	tests := []struct {
		name            string
		script          string
		limits          ResourceLimits
		expectedCode    int
		expectedVerdict Verdict
		checkExtraInfo  string // Substring to check in ExtraInfo
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
			checkExtraInfo:  "limit",
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
			name:            "OLE - excessive output",
			script:          "print('x' * 10000000)",
			limits:          largeLimits(),
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
			env := newStandardSandboxTestEnv(t)

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
		})
	}
}

func TestContainerdSandbox_IOOperations(t *testing.T) {
	tests := []struct {
		name        string
		setupMount  func(t *testing.T, tmpDir string) // Prepare files
		script      string
		stdin       string
		mountRO     bool
		checkResult func(t *testing.T, tmpDir string, result ExecuteResult)
	}{
		{
			name:       "stdin - read and process",
			setupMount: nil,
			script:     "import sys; print(sys.stdin.read(), end='')",
			stdin:      "test input\n",
			checkResult: func(t *testing.T, tmpDir string, result ExecuteResult) {
				assert.Equal(t, 0, result.ExitCode)
				assert.Equal(t, VerdictOK, result.Verdict)
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
			checkResult: func(t *testing.T, tmpDir string, result ExecuteResult) {
				assert.Equal(t, 0, result.ExitCode)
				assert.Equal(t, VerdictOK, result.Verdict)
				assert.Contains(t, result.Stdout, "49")
			},
		},
		{
			name:       "write file - create output",
			setupMount: nil,
			script: `
with open('/sandbox/result.txt', 'w') as f:
    f.write('test output')
print('done')
`,
			mountRO: false,
			checkResult: func(t *testing.T, tmpDir string, result ExecuteResult) {
				assert.Equal(t, 0, result.ExitCode)
				assert.Equal(t, VerdictOK, result.Verdict)

				content, err := os.ReadFile(filepath.Join(tmpDir, "result.txt"))
				require.NoError(t, err)
				assert.Equal(t, "test output", string(content))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newStandardSandboxTestEnv(t)
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

			if tmpDir != "" {
				req.MountDir = &Mount{
					HostPath:      tmpDir,
					ContainerPath: "/sandbox",
					ReadOnly:      tt.mountRO,
				}
			}

			result, err := env.sb.Execute(env.ctx, req)
			require.NoError(t, err)

			tt.checkResult(t, tmpDir, result)
		})
	}
}

func TestContainerdSandbox_Execute_WriteFile(t *testing.T) {
	env := newStandardSandboxTestEnv(t)

	tmpDir := t.TempDir()

	pythonCode := `
with open('/output/result.txt', 'w') as f:
    f.write('test output')
print('done')
`

	req := ExecuteRequest{
		ImageRef: testPythonImageRef,
		Command:  []string{"python3", "-c", pythonCode},
		MountDir: &Mount{
			HostPath:      tmpDir,
			ContainerPath: "/output",
			ReadOnly:      false,
		},
		Limits: standardLimits(),
	}

	result, err := env.sb.Execute(env.ctx, req)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, VerdictOK, result.Verdict)

	// 验证文件被创建
	outputPath := filepath.Join(tmpDir, "result.txt")
	content, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, "test output", string(content))
}

func TestContainerdSandbox_SeccompEnforcement(t *testing.T) {
	// NOTE: This test focuses on socket blocking as the primary seccomp verification.
	// Fork/vfork blocking is tested at the service layer with C programs (policy_fork_bomb.c)
	// because Python's os.fork() uses clone() which must be allowed for thread support.
	tests := []struct {
		name          string
		script        string
		enableSeccomp bool
		expectBlocked bool
		checkResult   func(t *testing.T, result ExecuteResult)
	}{
		{
			name: "seccomp enabled - socket blocked",
			script: `
import socket
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    print("socket succeeded")
except OSError as e:
    print(f"socket blocked: {e}")
`,
			enableSeccomp: true,
			expectBlocked: true,
			checkResult: func(t *testing.T, result ExecuteResult) {
				// Socket creation should fail with OSError
				assert.Contains(t, []Verdict{VerdictOK, VerdictRE}, result.Verdict)
				assert.Contains(t, result.Stdout, "socket blocked")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newStandardSandboxTestEnv(t)

			req := ExecuteRequest{
				ImageRef:      testPythonImageRef,
				Command:       []string{"python3", "-c", tt.script},
				Limits:        standardLimits(),
				EnableSeccomp: tt.enableSeccomp,
			}

			result, err := env.sb.Execute(env.ctx, req)
			require.NoError(t, err)

			tt.checkResult(t, result)
		})
	}
}
