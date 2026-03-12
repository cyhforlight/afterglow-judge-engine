package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSandbox struct {
	preflightErr error
	executeFunc  func(t *testing.T, req sandbox.ExecuteRequest) sandbox.ExecuteResult
	t            *testing.T
}

func (s *fakeSandbox) Execute(_ context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
	if s.executeFunc == nil {
		return sandbox.ExecuteResult{}, nil
	}
	return s.executeFunc(s.t, req), nil
}

func (s *fakeSandbox) PreflightCheck(_ context.Context) error {
	return s.preflightErr
}

func TestCompiler_LoadsArtifactFromWorkspace(t *testing.T) {
	sb := &fakeSandbox{
		t: t,
		executeFunc: func(t *testing.T, req sandbox.ExecuteRequest) sandbox.ExecuteResult {
			t.Helper()

			sourcePath := filepath.Join(req.MountDir.HostPath, "main.c")
			source, err := os.ReadFile(sourcePath)
			require.NoError(t, err)
			assert.Equal(t, "int main() { return 0; }", string(source))

			artifactPath := filepath.Join(req.MountDir.HostPath, "program")
			err = os.WriteFile(artifactPath, []byte("binary"), 0o755)
			require.NoError(t, err)

			return sandbox.ExecuteResult{
				ExitCode: 0,
				Verdict:  sandbox.VerdictOK,
				Stdout:   "build ok",
			}
		},
	}

	compiler := NewCompiler(sb)
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Files: []workspace.File{{
			Name:    "main.c",
			Content: []byte("int main() { return 0; }"),
			Mode:    0644,
		}},
		ImageRef:     "compiler-image",
		Command:      []string{"gcc", "-o", "/work/program", "/work/main.c"},
		ArtifactName: "program",
		ArtifactMode: 0o755,
		ArtifactPath: "program",
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	})
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)
	assert.Equal(t, []byte("binary"), out.Artifact.Data)
}

func TestRunner_WritesFilesAndReturnsRawResult(t *testing.T) {
	sb := &fakeSandbox{
		t: t,
		executeFunc: func(t *testing.T, req sandbox.ExecuteRequest) sandbox.ExecuteResult {
			t.Helper()

			programPath := filepath.Join(req.MountDir.HostPath, "program")
			program, err := os.ReadFile(programPath)
			require.NoError(t, err)
			assert.Equal(t, []byte("binary"), program)

			require.NotNil(t, req.Cwd)
			assert.Equal(t, runMountDir, *req.Cwd)
			assert.Equal(t, []string{"/sandbox/program"}, req.Command)

			return sandbox.ExecuteResult{
				ExitCode:  0,
				Stdout:    "stdout",
				Stderr:    "stderr",
				CPUTimeMs: 12,
				MemoryMB:  34,
				Verdict:   sandbox.VerdictOK,
				ExtraInfo: "details",
			}
		},
	}

	runner := NewRunner(sb)
	out, err := runner.Run(context.Background(), RunRequest{
		Files: []workspace.File{{
			Name:    "program",
			Content: []byte("binary"),
			Mode:    0o755,
		}},
		ImageRef: "runtime-image",
		Command:  []string{"/sandbox/program"},
		Cwd:      runMountDir,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   1000,
			WallTimeMs:  3000,
			MemoryMB:    128,
			OutputBytes: sandbox.DefaultExecutionOutputLimitBytes,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, RunResult{
		ExitCode:  0,
		Stdout:    "stdout",
		Stderr:    "stderr",
		CPUTimeMs: 12,
		MemoryMB:  34,
		Verdict:   sandbox.VerdictOK,
		ExtraInfo: "details",
	}, out)
}

func TestLoadPythonBytecodeArtifact_RenamesArtifact(t *testing.T) {
	dir := t.TempDir()
	pycacheDir := filepath.Join(dir, "__pycache__")
	require.NoError(t, os.Mkdir(pycacheDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pycacheDir, "solution.cpython-311.pyc"), []byte("pyc"), 0o644))

	loader := loadPythonBytecodeArtifact("solution.pyc", 0o644)
	artifact, err := loader(dir)
	require.NoError(t, err)
	assert.Equal(t, model.CompiledArtifact{
		Name: "solution.pyc",
		Data: []byte("pyc"),
		Mode: 0o644,
	}, artifact)
}
