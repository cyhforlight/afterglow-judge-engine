package execution

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"afterglow-judge-engine/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testImageRef     = "image"
	testWorkMount    = "/work"
	testSandboxMount = "/sandbox"
	testSourceName   = "main.c"
	testProgramName  = "program"
	testSource       = "int main() { return 0; }"
	testBinary       = "binary"
)

type fakeSandbox struct {
	executeFunc func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error)
}

func (s *fakeSandbox) Execute(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
	if s.executeFunc == nil {
		return sandbox.ExecuteResult{}, nil
	}
	return s.executeFunc(req)
}

func newTestExecutor(t testing.TB, sb sandboxExecutor, maxConcurrent int) Executor {
	t.Helper()
	exec, err := NewExecutor(sb, maxConcurrent)
	require.NoError(t, err)
	return exec
}

func TestExecutor_Compile(t *testing.T) {
	sb := &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()
			assertCompileSandboxRequest(t, req)
			return sandbox.ExecuteResult{
				ExitCode: 0,
				Stdout:   "stdout",
				Stderr:   "stderr",
				Verdict:  sandbox.VerdictOK,
			}, nil
		},
	}

	result, err := newTestExecutor(t, sb, 1).Compile(t.Context(), validCompileRequest())
	require.NoError(t, err)
	assert.Equal(t, "stdout\nstderr", result.Log)
	require.NotNil(t, result.Artifact)
	assert.Equal(t, testProgramName, result.Artifact.Name)
	assert.Equal(t, []byte(testBinary), result.Artifact.Data)
	assert.Equal(t, os.FileMode(0o755), result.Artifact.Mode)
}

func TestExecutor_CompileFailure(t *testing.T) {
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(_ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			return sandbox.ExecuteResult{
				ExitCode: 1,
				Stdout:   "stdout",
				Stderr:   "stderr",
				Verdict:  sandbox.VerdictRE,
			}, nil
		},
	}, 1)

	result, err := exec.Compile(t.Context(), validCompileRequest())
	require.NoError(t, err)
	assert.Equal(t, "stdout\nstderr", result.Log)
	assert.Nil(t, result.Artifact)
}

func TestExecutor_Run(t *testing.T) {
	sb := &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()
			require.NotNil(t, req.MountDir)
			assert.Equal(t, testSandboxMount, req.MountDir.ContainerPath)
			assert.True(t, req.MountDir.ReadOnly)
			assert.True(t, req.EnableSeccomp)

			stdin, err := io.ReadAll(req.Stdin)
			require.NoError(t, err)
			assert.Equal(t, "input", string(stdin))

			artifact, err := os.ReadFile(filepath.Join(req.MountDir.HostPath, testProgramName))
			require.NoError(t, err)
			assert.Equal(t, testBinary, string(artifact))

			return sandbox.ExecuteResult{
				ExitCode:  0,
				CPUTimeMs: 12,
				Verdict:   sandbox.VerdictOK,
			}, nil
		},
	}

	result, err := newTestExecutor(t, sb, 1).Run(t.Context(), validRunRequest())
	require.NoError(t, err)
	assert.Equal(t, 12, result.CPUTimeMs)
}

func TestExecutor_MissingArtifactReturnsError(t *testing.T) {
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(_ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()
			return sandbox.ExecuteResult{ExitCode: 0, Verdict: sandbox.VerdictOK}, nil
		},
	}, 1)

	req := validCompileRequest()
	req.ArtifactName = "missing"
	_, err := exec.Compile(t.Context(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `stat artifact "missing"`)
}

func TestExecutor_SandboxError(t *testing.T) {
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(_ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()
			return sandbox.ExecuteResult{}, errors.New("boom")
		},
	}, 1)

	_, err := exec.Compile(t.Context(), validCompileRequest())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox execute: boom")
}

type blockingSandbox struct {
	unblock    chan struct{}
	concurrent atomic.Int32
}

func (s *blockingSandbox) Execute(_ context.Context, _ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
	s.concurrent.Add(1)
	defer s.concurrent.Add(-1)

	<-s.unblock
	return sandbox.ExecuteResult{ExitCode: 1, Verdict: sandbox.VerdictRE}, nil
}

func TestExecutor_ConcurrencyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const limit = 2
		const calls = 5
		sb := &blockingSandbox{unblock: make(chan struct{})}
		exec := newTestExecutor(t, sb, limit)
		errs := make([]error, calls)

		for i := range calls {
			go func() {
				if i%2 == 0 {
					_, errs[i] = exec.Compile(t.Context(), validCompileRequest())
					return
				}
				_, errs[i] = exec.Run(t.Context(), validRunRequest())
			}()
		}

		synctest.Wait()
		assert.Equal(t, int32(limit), sb.concurrent.Load())

		close(sb.unblock)
		synctest.Wait()
		for _, err := range errs {
			require.NoError(t, err)
		}
	})
}

func TestExecutor_ContextCancelWhileWaitingForCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sb := &blockingSandbox{unblock: make(chan struct{})}
		exec := newTestExecutor(t, sb, 1)
		go exec.Run(t.Context(), validRunRequest())
		synctest.Wait()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := exec.Compile(ctx, validCompileRequest())
		require.ErrorIs(t, err, context.Canceled)

		close(sb.unblock)
	})
}

func TestNewExecutor_RequiresPositiveConcurrency(t *testing.T) {
	exec, err := NewExecutor(&fakeSandbox{}, 0)
	assert.Nil(t, exec)
	require.ErrorContains(t, err, "max concurrent executions must be positive")
}

func validCompileRequest() CompileRequest {
	return CompileRequest{
		Files:        oneFile(),
		ImageRef:     testImageRef,
		Command:      []string{"build"},
		ArtifactName: testProgramName,
		Limits: Limits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: DefaultCompileOutputLimitBytes,
		},
	}
}

func validRunRequest() RunRequest {
	return RunRequest{
		Artifact: Artifact{Name: testProgramName, Data: []byte(testBinary), Mode: 0o755},
		ImageRef: testImageRef,
		Command:  []string{testSandboxMount + "/" + testProgramName},
		Stdin:    strings.NewReader("input"),
		Limits: Limits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: DefaultRunOutputLimitBytes,
		},
	}
}

func oneFile() []File {
	return []File{{
		Name:    testSourceName,
		Content: []byte(testSource),
		Mode:    0o644,
	}}
}

func assertCompileSandboxRequest(t *testing.T, req sandbox.ExecuteRequest) {
	t.Helper()

	require.NotNil(t, req.MountDir)
	assert.Equal(t, testWorkMount, req.MountDir.ContainerPath)
	assert.False(t, req.MountDir.ReadOnly)
	assert.Equal(t, testImageRef, req.ImageRef)
	assert.Equal(t, []string{"build"}, req.Command)
	assert.False(t, req.EnableSeccomp)
	assert.Equal(t, sandbox.ResourceLimits{
		CPUTimeMs:   1000,
		WallTimeMs:  3000,
		MemoryMB:    128,
		OutputBytes: DefaultCompileOutputLimitBytes,
	}, req.Limits)

	source, err := os.ReadFile(filepath.Join(req.MountDir.HostPath, testSourceName))
	require.NoError(t, err)
	assert.Equal(t, testSource, string(source))

	err = os.WriteFile(filepath.Join(req.MountDir.HostPath, testProgramName), []byte(testBinary), 0o755)
	require.NoError(t, err)
}
