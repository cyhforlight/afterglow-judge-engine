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
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

const (
	testImageRef     = "image"
	testWorkMount    = "/work"
	testSandboxMount = "/sandbox"
	testCommand      = "cmd"
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

func (s *fakeSandbox) PreflightCheck(_ context.Context) error {
	return nil
}

func TestExecutor_WritesFilesAndCollectsArtifacts(t *testing.T) {
	sb := &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()

			assertCompileSandboxRequest(t, req)

			return sandbox.ExecuteResult{
				ExitCode:  0,
				Stdout:    "stdout",
				Stderr:    "stderr",
				CPUTimeMs: 12,
				MemoryMB:  34,
				Verdict:   sandbox.VerdictOK,
				ExtraInfo: "details",
			}, nil
		},
	}

	exec := NewExecutor(sb)
	result, err := exec.Execute(context.Background(), compileJobWithArtifact())
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "stdout", result.Stdout)
	assert.Equal(t, "stderr", result.Stderr)
	assert.Equal(t, 12, result.CPUTimeMs)
	assert.Equal(t, 34, result.MemoryMB)
	assert.Equal(t, VerdictOK, result.Verdict)
	assert.Equal(t, "details", result.ExtraInfo)
	require.Contains(t, result.Artifacts, testProgramName)
	assert.Equal(t, []byte(testBinary), result.Artifacts[testProgramName].Data)
	assert.Equal(t, os.FileMode(0o755), result.Artifacts[testProgramName].Mode)
}

func TestExecutor_PassesRuntimeOptions(t *testing.T) {
	sb := &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()

			require.NotNil(t, req.MountDir)
			assert.Equal(t, testSandboxMount, req.MountDir.ContainerPath)
			assert.True(t, req.MountDir.ReadOnly)
			require.NotNil(t, req.Cwd)
			assert.Equal(t, testSandboxMount, *req.Cwd)
			assert.True(t, req.EnableSeccomp)
			assert.NotNil(t, req.Stdin)

			stdin, err := io.ReadAll(req.Stdin)
			require.NoError(t, err)
			assert.Equal(t, "input", string(stdin))

			return sandbox.ExecuteResult{ExitCode: 0, Verdict: sandbox.VerdictOK}, nil
		},
	}

	exec := NewExecutor(sb)
	_, err := exec.Execute(context.Background(), Job{
		Files: []workspace.File{{
			Name:    testProgramName,
			Content: []byte(testBinary),
			Mode:    0o755,
		}},
		ImageRef:      testImageRef,
		Command:       []string{testSandboxMount + "/" + testProgramName},
		MountPath:     testSandboxMount,
		ReadOnlyMount: true,
		Cwd:           testSandboxMount,
		Stdin:         strings.NewReader("input"),
		Limits: Limits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: DefaultRunOutputLimitBytes,
		},
		EnableSeccomp: true,
	})
	require.NoError(t, err)
}

func TestExecutor_MissingArtifactReturnsError(t *testing.T) {
	exec := NewExecutor(&fakeSandbox{
		executeFunc: func(_ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()
			return sandbox.ExecuteResult{ExitCode: 0, Verdict: sandbox.VerdictOK}, nil
		},
	})

	_, err := exec.Execute(context.Background(), validJobWithArtifact("missing"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `stat artifact "missing"`)
}

func TestExecutor_SandboxErrorSkipsArtifactCollection(t *testing.T) {
	exec := NewExecutor(&fakeSandbox{
		executeFunc: func(_ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()
			return sandbox.ExecuteResult{}, errors.New("boom")
		},
	})

	_, err := exec.Execute(context.Background(), validJobWithArtifact(testProgramName))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox execute: boom")
}

func TestExecutor_ValidateJob(t *testing.T) {
	tests := []struct {
		name    string
		job     Job
		wantErr string
	}{
		{name: "missing image", job: Job{Command: []string{testCommand}, Files: oneFile(), MountPath: testWorkMount}, wantErr: "execution image is required"},
		{name: "missing command", job: Job{ImageRef: testImageRef, Files: oneFile(), MountPath: testWorkMount}, wantErr: "execution command is required"},
		{name: "missing files", job: Job{ImageRef: testImageRef, Command: []string{testCommand}, MountPath: testWorkMount}, wantErr: "at least one execution file is required"},
		{name: "missing mount path", job: Job{ImageRef: testImageRef, Command: []string{testCommand}, Files: oneFile()}, wantErr: "execution mount path is required"},
	}

	exec := NewExecutor(&fakeSandbox{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := exec.Execute(context.Background(), tt.job)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

type blockingExecutor struct {
	unblock    chan struct{}
	concurrent atomic.Int32
}

func (e *blockingExecutor) PreflightCheck(_ context.Context) error { return nil }

func (e *blockingExecutor) Execute(_ context.Context, _ Job) (Result, error) {
	e.concurrent.Add(1)
	defer e.concurrent.Add(-1)

	<-e.unblock
	return Result{}, nil
}

func TestThrottledExecutor_ConcurrencyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const limit = 2
		sem := semaphore.NewWeighted(limit)
		inner := &blockingExecutor{unblock: make(chan struct{})}
		throttled := NewThrottledExecutor(inner, sem)

		for range 5 {
			go throttled.Execute(t.Context(), Job{})
		}

		synctest.Wait()
		assert.Equal(t, int32(limit), inner.concurrent.Load())

		close(inner.unblock)
	})
}

func TestThrottledExecutor_ContextCancel(t *testing.T) {
	sem := semaphore.NewWeighted(1)
	require.NoError(t, sem.Acquire(t.Context(), 1))
	throttled := NewThrottledExecutor(&blockingExecutor{unblock: make(chan struct{})}, sem)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := throttled.Execute(ctx, Job{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestNewThrottledExecutor_RequiresSemaphore(t *testing.T) {
	assert.PanicsWithValue(t, "semaphore is required", func() {
		NewThrottledExecutor(&blockingExecutor{unblock: make(chan struct{})}, nil)
	})
}

func validJobWithArtifact(name string) Job {
	return Job{
		Files:     oneFile(),
		ImageRef:  testImageRef,
		Command:   []string{testCommand},
		MountPath: testWorkMount,
		Limits: Limits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: DefaultCompileOutputLimitBytes,
		},
		Artifacts: []string{name},
	}
}

func oneFile() []workspace.File {
	return []workspace.File{{
		Name:    testSourceName,
		Content: []byte(testSource),
		Mode:    0o644,
	}}
}

func compileJobWithArtifact() Job {
	return Job{
		Files:         oneFile(),
		ImageRef:      testImageRef,
		Command:       []string{"build"},
		MountPath:     testWorkMount,
		ReadOnlyMount: false,
		Cwd:           testWorkMount,
		Limits: Limits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: DefaultCompileOutputLimitBytes,
		},
		Artifacts: []string{testProgramName},
	}
}

func assertCompileSandboxRequest(t *testing.T, req sandbox.ExecuteRequest) {
	t.Helper()

	require.NotNil(t, req.MountDir)
	assert.Equal(t, testWorkMount, req.MountDir.ContainerPath)
	assert.False(t, req.MountDir.ReadOnly)
	assert.Equal(t, testImageRef, req.ImageRef)
	assert.Equal(t, []string{"build"}, req.Command)
	require.NotNil(t, req.Cwd)
	assert.Equal(t, testWorkMount, *req.Cwd)
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
