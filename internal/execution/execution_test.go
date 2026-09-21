package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"afterglow-judge-engine/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSandbox struct {
	executeFunc func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error)
}

func (s *fakeSandbox) Execute(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
	return s.executeFunc(req)
}

func newTestExecutor(t testing.TB, sb sandboxExecutor, maxConcurrent int) Executor {
	t.Helper()
	exec, err := NewExecutor(sb, maxConcurrent)
	require.NoError(t, err)
	return exec
}

func TestExecutor_MissingArtifactReturnsError(t *testing.T) {
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(_ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			t.Helper()
			return sandbox.ExecuteResult{ExitCode: 0, Verdict: sandbox.VerdictOK}, nil
		},
	}, 1)

	_, err := exec.Compile(t.Context(), CompileRequest{ArtifactName: "missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `stat artifact "missing"`)
}

func TestExecutor_CompileRejectsArtifactOverSizeLimit(t *testing.T) {
	const artifactSize = 64*1024*1024 + 1
	var workspacePath string
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			workspacePath = req.MountDir.HostPath
			artifact, err := os.Create(filepath.Join(workspacePath, "program"))
			require.NoError(t, err)
			require.NoError(t, artifact.Truncate(artifactSize))
			require.NoError(t, artifact.Close())
			return sandbox.ExecuteResult{
				ExitCode: 0,
				Verdict:  sandbox.VerdictOK,
				Stdout:   "compiler stdout",
				Stderr:   "compiler warning",
			}, nil
		},
	}, 1)

	result, err := exec.Compile(t.Context(), CompileRequest{ArtifactName: "program"})

	require.NoError(t, err)
	if result.Artifact != nil {
		t.Fatal("oversized compilation artifact was accepted")
	}
	assert.Equal(t, "compiler stdout\ncompiler warning\ncompiled artifact \"program\" exceeds size limit (67108865 bytes > 67108864 bytes)", result.Log)
	_, err = os.Stat(workspacePath)
	assert.True(t, os.IsNotExist(err))
}

func TestExecutor_CompileAcceptsArtifactAtSizeLimit(t *testing.T) {
	const artifactSize = 64 * 1024 * 1024
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			artifact, err := os.OpenFile(filepath.Join(req.MountDir.HostPath, "program"), os.O_CREATE|os.O_WRONLY, 0o755)
			require.NoError(t, err)
			require.NoError(t, artifact.Truncate(artifactSize))
			_, err = artifact.WriteAt([]byte("binary"), 0)
			require.NoError(t, err)
			_, err = artifact.WriteAt([]byte{0x7f}, artifactSize-1)
			require.NoError(t, err)
			require.NoError(t, artifact.Close())
			return sandbox.ExecuteResult{ExitCode: 0, Verdict: sandbox.VerdictOK}, nil
		},
	}, 1)

	result, err := exec.Compile(t.Context(), CompileRequest{ArtifactName: "program"})

	require.NoError(t, err)
	require.NotNil(t, result.Artifact)
	assert.Empty(t, result.Log)
	assert.Equal(t, "program", result.Artifact.Name)
	assert.Equal(t, os.FileMode(0o755), result.Artifact.Mode)
	require.Len(t, result.Artifact.Data, artifactSize)
	assert.Equal(t, []byte("binary"), result.Artifact.Data[:6])
	assert.Equal(t, byte(0x7f), result.Artifact.Data[artifactSize-1])
}

func TestExecutor_CompileRejectsNonRegularArtifact(t *testing.T) {
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			require.NoError(t, os.Mkdir(filepath.Join(req.MountDir.HostPath, "program"), 0o755))
			return sandbox.ExecuteResult{ExitCode: 0, Verdict: sandbox.VerdictOK}, nil
		},
	}, 1)

	result, err := exec.Compile(t.Context(), CompileRequest{ArtifactName: "program"})

	require.NoError(t, err)
	assert.Nil(t, result.Artifact)
	assert.Equal(t, `compiled artifact "program" is not a regular file`, result.Log)
}

func TestExecutor_CompilePreservesFailureDiagnostics(t *testing.T) {
	tests := []struct {
		name    string
		result  sandbox.ExecuteResult
		wantLog string
	}{
		{
			name:    "silent time limit",
			result:  sandbox.ExecuteResult{Verdict: sandbox.VerdictTLE, ExitCode: 137, ExtraInfo: "CPU time exceeded: 35ms >= 30ms"},
			wantLog: "CPU time exceeded: 35ms >= 30ms",
		},
		{
			name:    "memory limit with compiler diagnostics",
			result:  sandbox.ExecuteResult{Verdict: sandbox.VerdictMLE, ExitCode: 137, Stdout: "warning", Stderr: "compiler stopped", ExtraInfo: "memory limit exceeded (peak 512MB, limit 512MB)"},
			wantLog: "warning\ncompiler stopped\nmemory limit exceeded (peak 512MB, limit 512MB)",
		},
		{
			name:    "output limit with truncated diagnostics",
			result:  sandbox.ExecuteResult{Verdict: sandbox.VerdictOLE, ExitCode: 137, Stderr: "truncated diagnostics", ExtraInfo: "output limit exceeded (1024 bytes max)"},
			wantLog: "truncated diagnostics\noutput limit exceeded (1024 bytes max)",
		},
		{
			name:    "ordinary compiler error appears once",
			result:  sandbox.ExecuteResult{Verdict: sandbox.VerdictRE, ExitCode: 1, Stdout: "warning", Stderr: "syntax error", ExtraInfo: "syntax error"},
			wantLog: "warning\nsyntax error",
		},
		{
			name:    "silent nonzero exit",
			result:  sandbox.ExecuteResult{Verdict: sandbox.VerdictRE, ExitCode: 2},
			wantLog: "compiler exited with code 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := newTestExecutor(t, &fakeSandbox{
				executeFunc: func(sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) { return tt.result, nil },
			}, 1)
			result, err := exec.Compile(t.Context(), CompileRequest{ArtifactName: "program"})
			require.NoError(t, err)
			assert.Nil(t, result.Artifact)
			assert.Equal(t, tt.wantLog, result.Log)
		})
	}
}

func TestExecutor_CleansWorkspaceAfterSandboxError(t *testing.T) {
	sandboxErr := errors.New("sandbox failed")
	var hostPath string
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			require.NotNil(t, req.MountDir)
			hostPath = req.MountDir.HostPath
			return sandbox.ExecuteResult{}, sandboxErr
		},
	}, 1)

	_, err := exec.Compile(t.Context(), CompileRequest{})
	require.ErrorIs(t, err, sandboxErr)
	require.NotEmpty(t, hostPath)

	_, err = os.Stat(hostPath)
	assert.True(t, os.IsNotExist(err))
}

func TestExecutor_RejectsArtifactSymlinkEscape(t *testing.T) {
	const artifactName = "program"
	outsidePath := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outsidePath, []byte("host data"), 0o644))

	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			require.NotNil(t, req.MountDir)
			require.NoError(t, os.Symlink(outsidePath, filepath.Join(req.MountDir.HostPath, artifactName)))
			return sandbox.ExecuteResult{ExitCode: 0, Verdict: sandbox.VerdictOK}, nil
		},
	}, 1)

	result, err := exec.Compile(t.Context(), CompileRequest{ArtifactName: artifactName})

	require.ErrorContains(t, err, `stat artifact "program"`)
	assert.Nil(t, result.Artifact)
}

func TestExecutor_RunUsesReadOnlyWorkspace(t *testing.T) {
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			require.NotNil(t, req.MountDir)
			assert.True(t, req.MountDir.ReadOnly)
			return sandbox.ExecuteResult{ExitCode: 1, Verdict: sandbox.VerdictRE}, nil
		},
	}, 1)

	_, err := exec.Run(t.Context(), RunRequest{
		Artifact: Artifact{Name: "program", Mode: 0o755},
	})
	require.NoError(t, err)
}

func TestExecutor_UnknownSandboxVerdictReturnsError(t *testing.T) {
	exec := newTestExecutor(t, &fakeSandbox{
		executeFunc: func(_ sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
			return sandbox.ExecuteResult{}, nil
		},
	}, 1)

	_, err := exec.Compile(t.Context(), CompileRequest{})

	require.ErrorContains(t, err, "sandbox execute returned unknown verdict")
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
					_, errs[i] = exec.Compile(t.Context(), CompileRequest{})
					return
				}
				_, errs[i] = exec.Run(t.Context(), RunRequest{
					Artifact: Artifact{Name: "program", Mode: 0o755},
				})
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
		go exec.Run(t.Context(), RunRequest{
			Artifact: Artifact{Name: "program", Mode: 0o755},
		})
		synctest.Wait()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := exec.Compile(ctx, CompileRequest{})
		require.ErrorIs(t, err, context.Canceled)

		close(sb.unblock)
	})
}
