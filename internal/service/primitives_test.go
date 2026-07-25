package service

import (
	"context"
	"testing"

	"afterglow-judge-engine/internal/execution"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExecutor struct {
	executeFunc func(job execution.Job) execution.Result
}

func (e *fakeExecutor) Execute(_ context.Context, job execution.Job) (execution.Result, error) {
	if e.executeFunc == nil {
		return execution.Result{}, nil
	}
	return e.executeFunc(job), nil
}

func TestCompiler_UsesWritableUnsandboxedWorkspace(t *testing.T) {
	exec := &fakeExecutor{
		executeFunc: func(job execution.Job) execution.Result {
			t.Helper()

			assert.Equal(t, "/work", job.MountPath)
			assert.False(t, job.ReadOnlyMount)
			assert.False(t, job.EnableSeccomp)
			assert.Equal(t, []string{"program"}, job.Artifacts)
			return execution.Result{
				RawResult: execution.RawResult{Verdict: execution.VerdictOK},
				Artifacts: map[string]execution.Artifact{
					"program": {Data: []byte("binary"), Mode: 0o755},
				},
			}
		},
	}

	compiler := newCompiler(exec)
	out, err := compiler.Compile(t.Context(), CompileRequest{
		ArtifactName: "program",
	})
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)
}

func TestRunner_UsesReadOnlySandbox(t *testing.T) {
	exec := &fakeExecutor{
		executeFunc: func(job execution.Job) execution.Result {
			t.Helper()

			assert.Equal(t, "/sandbox", job.MountPath)
			assert.True(t, job.ReadOnlyMount)
			assert.True(t, job.EnableSeccomp)
			return execution.Result{}
		},
	}

	runner := newRunner(exec)
	_, err := runner.Run(t.Context(), RunRequest{})
	require.NoError(t, err)
}
