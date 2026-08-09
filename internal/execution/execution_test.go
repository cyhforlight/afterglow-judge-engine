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
