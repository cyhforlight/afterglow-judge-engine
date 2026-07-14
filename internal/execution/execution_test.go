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

	exec := NewExecutor(sb, 1)
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
			assert.True(t, req.EnableSeccomp)
			assert.NotNil(t, req.Stdin)

			stdin, err := io.ReadAll(req.Stdin)
			require.NoError(t, err)
			assert.Equal(t, "input", string(stdin))

			return sandbox.ExecuteResult{ExitCode: 0, Verdict: sandbox.VerdictOK}, nil
		},
	}

	exec := NewExecutor(sb, 1)
	_, err := exec.Execute(context.Background(), Job{
		Files: []File{{
			Name:    testProgramName,
			Content: []byte(testBinary),
			Mode:    0o755,
		}},
		ImageRef:      testImageRef,
		Command:       []string{testSandboxMount + "/" + testProgramName},
		MountPath:     testSandboxMount,
		ReadOnlyMount: true,
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
	}, 1)

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
	}, 1)

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

	exec := NewExecutor(&fakeSandbox{}, 1)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := exec.Execute(context.Background(), tt.job)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

type blockingSandbox struct {
	unblock    chan struct{}
	concurrent atomic.Int32
}

func (s *blockingSandbox) Execute(_ context.Context, _ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
	s.concurrent.Add(1)
	defer s.concurrent.Add(-1)

	<-s.unblock
	return sandbox.ExecuteResult{}, nil
}

func TestExecutor_ConcurrencyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const limit = 2
		sb := &blockingSandbox{unblock: make(chan struct{})}
		exec := NewExecutor(sb, limit)

		for range 5 {
			go exec.Execute(t.Context(), validJob())
		}

		synctest.Wait()
		assert.Equal(t, int32(limit), sb.concurrent.Load())

		close(sb.unblock)
	})
}

func TestExecutor_ContextCancelWhileWaitingForCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sb := &blockingSandbox{unblock: make(chan struct{})}
		exec := NewExecutor(sb, 1)
		go exec.Execute(t.Context(), validJob())
		synctest.Wait()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := exec.Execute(ctx, validJob())
		require.ErrorIs(t, err, context.Canceled)

		close(sb.unblock)
	})
}

func TestNewExecutor_RequiresPositiveConcurrency(t *testing.T) {
	assert.PanicsWithValue(t, "max concurrent executions must be positive", func() {
		NewExecutor(&fakeSandbox{}, 0)
	})
}

func validJobWithArtifact(name string) Job {
	job := validJob()
	job.Artifacts = []string{name}
	return job
}

func validJob() Job {
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
	}
}

func oneFile() []File {
	return []File{{
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
