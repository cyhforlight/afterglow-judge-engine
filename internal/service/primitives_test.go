package service

import (
	"context"
	"testing"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeExecutor struct {
	preflightErr error
	executeFunc  func(job execution.Job) execution.Result
}

func (e *fakeExecutor) Execute(_ context.Context, job execution.Job) (execution.Result, error) {
	if e.executeFunc == nil {
		return execution.Result{}, nil
	}
	return e.executeFunc(job), nil
}

func (e *fakeExecutor) PreflightCheck(_ context.Context) error {
	return e.preflightErr
}

func TestCompiler_ExecutesCompileJobAndLoadsArtifact(t *testing.T) {
	exec := &fakeExecutor{
		executeFunc: func(job execution.Job) execution.Result {
			t.Helper()

			assert.Equal(t, "compiler-image", job.ImageRef)
			assert.Equal(t, []string{"gcc", "-o", "/work/program", "/work/main.c"}, job.Command)
			assert.Equal(t, compileMountDir, job.MountPath)
			assert.False(t, job.ReadOnlyMount)
			assert.False(t, job.EnableSeccomp)
			assert.Equal(t, []string{"program"}, job.Artifacts)
			require.Len(t, job.Files, 1)
			assert.Equal(t, "main.c", job.Files[0].Name)
			assert.Equal(t, []byte("int main() { return 0; }"), job.Files[0].Content)

			return execution.Result{
				RawResult: execution.RawResult{
					ExitCode: 0,
					Verdict:  execution.VerdictOK,
					Stdout:   "build ok",
				},
				Artifacts: map[string]execution.Artifact{
					"program": {Data: []byte("binary"), Mode: 0o755},
				},
			}
		},
	}

	compiler := NewCompiler(exec)
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Files: []workspace.File{{
			Name:    "main.c",
			Content: []byte("int main() { return 0; }"),
			Mode:    0o644,
		}},
		ImageRef:     "compiler-image",
		Command:      []string{"gcc", "-o", "/work/program", "/work/main.c"},
		ArtifactName: "program",
		Limits: execution.Limits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: execution.DefaultCompileOutputLimitBytes,
		},
	})
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)
	assert.Equal(t, []byte("binary"), out.Artifact.Data)
}

func TestRunner_ExecutesRunJobAndReturnsRawResult(t *testing.T) {
	exec := &fakeExecutor{
		executeFunc: func(job execution.Job) execution.Result {
			t.Helper()

			assert.Equal(t, "runtime-image", job.ImageRef)
			assert.Equal(t, []string{"/sandbox/program"}, job.Command)
			assert.Equal(t, runMountDir, job.MountPath)
			assert.True(t, job.ReadOnlyMount)
			assert.True(t, job.EnableSeccomp)
			require.Len(t, job.Files, 1)
			assert.Equal(t, "program", job.Files[0].Name)
			assert.Equal(t, []byte("binary"), job.Files[0].Content)

			return execution.Result{
				RawResult: execution.RawResult{
					ExitCode:  0,
					Stdout:    "stdout",
					Stderr:    "stderr",
					CPUTimeMs: 12,
					MemoryMB:  34,
					Verdict:   execution.VerdictOK,
					ExtraInfo: "details",
				},
			}
		},
	}

	runner := NewRunner(exec)
	out, err := runner.Run(context.Background(), RunRequest{
		Files: []workspace.File{{
			Name:    "program",
			Content: []byte("binary"),
			Mode:    0o755,
		}},
		ImageRef: "runtime-image",
		Command:  []string{"/sandbox/program"},
		Limits: execution.Limits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: execution.DefaultRunOutputLimitBytes,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, RunResult{
		ExitCode:  0,
		Stdout:    "stdout",
		Stderr:    "stderr",
		CPUTimeMs: 12,
		MemoryMB:  34,
		Verdict:   execution.VerdictOK,
		ExtraInfo: "details",
	}, out)
}
