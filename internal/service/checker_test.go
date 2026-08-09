package service

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkerExecutorFake struct {
	compileResult   execution.CompileResult
	compileErr      error
	compileRequests []execution.CompileRequest
	runResult       execution.RunResult
}

func (e *checkerExecutorFake) Compile(
	_ context.Context,
	req execution.CompileRequest,
) (execution.CompileResult, error) {
	e.compileRequests = append(e.compileRequests, req)
	return e.compileResult, e.compileErr
}

func (e *checkerExecutorFake) Run(
	_ context.Context,
	_ execution.RunRequest,
) (execution.RunResult, error) {
	return e.runResult, nil
}

func checkerTestFS() fstest.MapFS {
	return testFileSystem(map[string][]byte{
		"checkers/default.cpp": []byte("checker source"),
		testlibHeaderKey:       []byte("testlib header"),
	})
}

func TestNewChecker_RejectsMissingTestlib(t *testing.T) {
	bundledFS := testFileSystem(map[string][]byte{"checkers/default.cpp": []byte("source")})
	_, err := newChecker(&checkerExecutorFake{}, bundledFS, nil)

	require.ErrorContains(t, err, `checker dependency "testlib.h" is not available`)
}

func TestNewChecker_RejectsMissingDefaultChecker(t *testing.T) {
	bundledFS := testFileSystem(map[string][]byte{testlibHeaderKey: []byte("header")})
	_, err := newChecker(&checkerExecutorFake{}, bundledFS, nil)

	require.ErrorContains(t, err, `checker dependency "checkers/default.cpp" is not available`)
}

func TestResolveChecker_Builtin(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPath string
		wantErr  string
	}{
		{name: "empty selects default", input: "", wantPath: "default"},
		{name: "valid name", input: "ncmp", wantPath: "ncmp"},
		{name: "uppercase allowed", input: "NCMP", wantPath: "NCMP"},
		{name: "underscore allowed", input: "my_checker", wantPath: "my_checker"},
		{name: "hyphen allowed", input: "ncmp-v2", wantPath: "ncmp-v2"},
		{name: "file extension rejected", input: "ncmp.cpp", wantErr: "invalid path characters"},
		{name: "path rejected", input: "../ncmp", wantErr: "invalid path characters"},
		{name: "special char rejected", input: "ncmp@v2", wantErr: "invalid characters"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			location, err := resolveChecker(tt.input)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, location.path)
			assert.False(t, location.isExternal)
		})
	}
}

func TestResolveChecker_External(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPath string
		wantErr  string
	}{
		{name: "valid path", input: "external:testcase-15/checker.cpp", wantPath: "testcase-15/checker.cpp"},
		{name: "normalized path", input: "external:a/../b/checker.cpp", wantPath: "b/checker.cpp"},
		{name: "path traversal rejected", input: "external:../etc/passwd", wantErr: "escapes resource root"},
		{name: "non-cpp rejected", input: "external:script.sh", wantErr: "must be a .cpp file"},
		{name: "empty path", input: "external:", wantErr: "external checker path is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			location, err := resolveChecker(tt.input)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, location.path)
			assert.True(t, location.isExternal)
		})
	}
}

func TestCheckerEngine_Materialize(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		bundledFS  fs.FS
		externalFS fs.FS
		wantErr    string
	}{
		{
			name:      "requested builtin missing",
			reference: "ncmp",
			bundledFS: checkerTestFS(),
			wantErr:   `builtin checker "ncmp" is not available`,
		},
		{
			name:      "external resources not configured",
			reference: "external:custom.cpp",
			bundledFS: checkerTestFS(),
			wantErr:   `external checker "custom.cpp" requires external resources`,
		},
		{
			name:       "external checker missing",
			reference:  "external:custom.cpp",
			bundledFS:  checkerTestFS(),
			externalFS: testFileSystem(nil),
			wantErr:    `external checker "custom.cpp" is not available`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &checkerEngine{bundledFS: tt.bundledFS, externalFS: tt.externalFS}
			location, err := resolveChecker(tt.reference)
			require.NoError(t, err)
			_, err = engine.Materialize(location)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestCheckerSnapshot_PrepareFailures(t *testing.T) {
	tests := []struct {
		name       string
		output     execution.CompileResult
		compileErr error
		wantErr    string
	}{
		{
			name:       "compiler infrastructure error",
			compileErr: errors.New("compiler unavailable"),
			wantErr:    "checker setup failed: compiler unavailable",
		},
		{
			name:    "compilation failed",
			output:  execution.CompileResult{Log: "syntax error"},
			wantErr: "checker compilation failed: syntax error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &checkerExecutorFake{compileResult: tt.output, compileErr: tt.compileErr}
			engine := &checkerEngine{executor: executor, bundledFS: checkerTestFS()}
			plan, err := engine.Materialize(checkerLocation{path: defaultCheckerName})
			require.NoError(t, err)

			_, err = plan.Prepare(t.Context())
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestCheckerSnapshot_PrepareUsesCapturedSource(t *testing.T) {
	const checkerSource = "original source"

	externalFS := testFileSystem(map[string][]byte{"custom.cpp": []byte(checkerSource)})
	executor := &checkerExecutorFake{compileResult: execution.CompileResult{
		Artifact: &execution.Artifact{Name: checkerArtifactName, Data: []byte("checker binary"), Mode: 0o755},
	}}
	engine := &checkerEngine{
		executor:      executor,
		bundledFS:     checkerTestFS(),
		externalFS:    externalFS,
		testlibHeader: []byte("testlib header"),
	}
	plan, err := engine.Materialize(checkerLocation{isExternal: true, path: "custom.cpp"})
	require.NoError(t, err)

	delete(externalFS, "custom.cpp")

	_, err = plan.Prepare(t.Context())
	require.NoError(t, err)
	require.Len(t, executor.compileRequests, 1)
	require.NotEmpty(t, executor.compileRequests[0].Files)
	assert.Equal(t, checkerSource, string(executor.compileRequests[0].Files[0].Content))
}

func TestCompiledChecker_Check(t *testing.T) {
	tests := []struct {
		name        string
		runResult   execution.RunResult
		wantVerdict model.Verdict
		wantMessage string
	}{
		{
			name:        "accepted with stderr message",
			runResult:   execution.RunResult{Verdict: execution.VerdictOK, ExitCode: 0, Stderr: " accepted "},
			wantVerdict: model.VerdictOK,
			wantMessage: "accepted",
		},
		{
			name:        "wrong answer exit one",
			runResult:   execution.RunResult{Verdict: execution.VerdictRE, ExitCode: 1, Stdout: "wrong"},
			wantVerdict: model.VerdictWA,
			wantMessage: "wrong",
		},
		{
			name:        "wrong answer exit two",
			runResult:   execution.RunResult{Verdict: execution.VerdictRE, ExitCode: 2, ExtraInfo: "presentation"},
			wantVerdict: model.VerdictWA,
			wantMessage: "presentation",
		},
		{
			name:        "sandbox timeout",
			runResult:   execution.RunResult{Verdict: execution.VerdictTLE, ExitCode: 0, Stderr: "timed out"},
			wantVerdict: model.VerdictUKE,
			wantMessage: "timed out",
		},
		{
			name:        "nonzero protocol exit",
			runResult:   execution.RunResult{Verdict: execution.VerdictRE, ExitCode: 3},
			wantVerdict: model.VerdictUKE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &checkerExecutorFake{runResult: tt.runResult}
			prepared := &compiledChecker{
				executor: executor,
				artifact: execution.Artifact{Name: checkerArtifactName, Data: []byte("binary"), Mode: 0o755},
			}

			result, err := prepared.Check(t.Context(), "input", "actual", "expected")
			require.NoError(t, err)
			assert.Equal(t, tt.wantVerdict, result.Verdict)
			assert.Equal(t, tt.wantMessage, result.Message)
		})
	}
}
