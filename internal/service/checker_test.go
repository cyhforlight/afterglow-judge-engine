package service

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingCheckerCompiler struct {
	mu       sync.Mutex
	output   CompileOutput
	err      error
	requests []CompileRequest
}

func (c *recordingCheckerCompiler) Compile(_ context.Context, req CompileRequest) (CompileOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	return c.output, c.err
}

type recordingCheckerRunner struct {
	mu       sync.Mutex
	result   RunResult
	err      error
	requests []RunRequest
}

func (r *recordingCheckerRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
	return r.result, r.err
}

func checkerTestFS() fstest.MapFS {
	return testFileSystem(map[string][]byte{
		"checkers/default.cpp": []byte("checker source"),
		testlibHeaderKey:       []byte("testlib header"),
	})
}

func TestNewChecker_RejectsMissingTestlib(t *testing.T) {
	bundledFS := testFileSystem(map[string][]byte{"checkers/default.cpp": []byte("source")})
	_, err := newChecker(&recordingCheckerCompiler{}, &recordingCheckerRunner{}, bundledFS, nil)

	require.ErrorContains(t, err, `checker dependency "testlib.h" is not available`)
}

func TestNewChecker_RejectsMissingDefaultChecker(t *testing.T) {
	bundledFS := testFileSystem(map[string][]byte{testlibHeaderKey: []byte("header")})
	_, err := newChecker(&recordingCheckerCompiler{}, &recordingCheckerRunner{}, bundledFS, nil)

	require.ErrorContains(t, err, `checker dependency "checkers/default.cpp" is not available`)
}

func TestNewChecker_RejectsDirectoryDependency(t *testing.T) {
	bundledFS := checkerTestFS()
	bundledFS[testlibHeaderKey] = &fstest.MapFile{Mode: fs.ModeDir}

	_, err := newChecker(&recordingCheckerCompiler{}, &recordingCheckerRunner{}, bundledFS, nil)

	require.ErrorContains(t, err, `"testlib.h" is not a regular file`)
}

func TestResolveChecker_Default(t *testing.T) {
	location, err := resolveChecker("")
	require.NoError(t, err)
	assert.Equal(t, "default", location.path)
	assert.False(t, location.isExternal)
}

func TestResolveChecker_Builtin(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPath string
		wantErr  string
	}{
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

func TestCheckerReference_Validate(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		bundledFS  fs.FS
		externalFS fs.FS
		wantErr    string
	}{
		{
			name:      "builtin available",
			bundledFS: checkerTestFS(),
		},
		{
			name:      "requested builtin missing",
			reference: "ncmp",
			bundledFS: checkerTestFS(),
			wantErr:   `builtin checker "ncmp" is not available`,
		},
		{
			name:      "engine dependency is not request validation",
			bundledFS: testFileSystem(map[string][]byte{"checkers/default.cpp": []byte("source")}),
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
		{
			name:       "external checker available",
			reference:  "external:custom.cpp",
			bundledFS:  checkerTestFS(),
			externalFS: testFileSystem(map[string][]byte{"custom.cpp": []byte("source")}),
		},
		{
			name:       "external checker is a directory",
			reference:  "external:custom.cpp",
			bundledFS:  checkerTestFS(),
			externalFS: fstest.MapFS{"custom.cpp": &fstest.MapFile{Mode: fs.ModeDir}},
			wantErr:    `"custom.cpp" is not a regular file`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &checkerEngine{bundledFS: tt.bundledFS, externalFS: tt.externalFS}
			resolved, err := engine.Resolve(tt.reference)
			require.NoError(t, err)

			err = resolved.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestCheckerReference_Prepare(t *testing.T) {
	compiler := &recordingCheckerCompiler{output: CompileOutput{
		Result:   model.CompileResult{Succeeded: true},
		Artifact: &execution.Artifact{Data: []byte("checker binary"), Mode: 0o755},
	}}
	runner := &recordingCheckerRunner{}
	engine := &checkerEngine{
		compiler:  compiler,
		runner:    runner,
		bundledFS: checkerTestFS(),
	}
	resolved, err := engine.Resolve("")
	require.NoError(t, err)

	prepared, err := resolved.Prepare(context.Background())
	require.NoError(t, err)
	require.NotNil(t, prepared)
	require.Len(t, compiler.requests, 1)

	req := compiler.requests[0]
	assert.Equal(t, "checker", req.ArtifactName)
	assert.Equal(t, "docker.io/library/gcc:12-bookworm", req.ImageRef)
	assert.Equal(t, checkerCompileProfile().BuildCommand, req.Command)
	assert.Equal(t, checkerCompileProfile().TimeoutMs, req.Limits.CPUTimeMs)
	assert.Equal(t, checkerCompileProfile().MemoryMB, req.Limits.MemoryMB)
	require.Len(t, req.Files, 2)
	assert.Equal(t, "checker.cpp", req.Files[0].Name)
	assert.Equal(t, []byte("checker source"), req.Files[0].Content)
	assert.Equal(t, testlibHeaderKey, req.Files[1].Name)
	assert.Equal(t, []byte("testlib header"), req.Files[1].Content)
}

func TestCheckerReference_PrepareFailures(t *testing.T) {
	tests := []struct {
		name       string
		output     CompileOutput
		compileErr error
		wantErr    string
	}{
		{
			name:       "compiler infrastructure error",
			compileErr: errors.New("compiler unavailable"),
			wantErr:    "checker setup failed: compiler unavailable",
		},
		{
			name: "compilation failed",
			output: CompileOutput{Result: model.CompileResult{
				Succeeded: false,
				Log:       "syntax error",
			}},
			wantErr: "checker compilation failed: syntax error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiler := &recordingCheckerCompiler{output: tt.output, err: tt.compileErr}
			engine := &checkerEngine{compiler: compiler, bundledFS: checkerTestFS()}
			resolved, err := engine.Resolve("")
			require.NoError(t, err)

			_, err = resolved.Prepare(context.Background())
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestCheckerReference_PrepareReportsDisappearedExternalSource(t *testing.T) {
	externalFS := testFileSystem(map[string][]byte{"custom.cpp": []byte("source")})
	engine := &checkerEngine{
		compiler:   &recordingCheckerCompiler{},
		bundledFS:  checkerTestFS(),
		externalFS: externalFS,
	}
	resolved, err := engine.Resolve("external:custom.cpp")
	require.NoError(t, err)
	require.NoError(t, resolved.Validate())

	delete(externalFS, "custom.cpp")

	_, err = resolved.Prepare(t.Context())
	require.ErrorContains(t, err, `checker setup failed: load external checker "custom.cpp"`)
}

func TestCompiledChecker_Check(t *testing.T) {
	tests := []struct {
		name        string
		runResult   RunResult
		wantVerdict model.Verdict
		wantMessage string
	}{
		{
			name:        "accepted with stderr message",
			runResult:   RunResult{Verdict: execution.VerdictOK, ExitCode: 0, Stderr: " accepted "},
			wantVerdict: model.VerdictOK,
			wantMessage: "accepted",
		},
		{
			name:        "wrong answer exit one",
			runResult:   RunResult{Verdict: execution.VerdictRE, ExitCode: 1, Stdout: "wrong"},
			wantVerdict: model.VerdictWA,
			wantMessage: "wrong",
		},
		{
			name:        "wrong answer exit two",
			runResult:   RunResult{Verdict: execution.VerdictRE, ExitCode: 2, ExtraInfo: "presentation"},
			wantVerdict: model.VerdictWA,
			wantMessage: "presentation",
		},
		{
			name:        "sandbox timeout",
			runResult:   RunResult{Verdict: execution.VerdictTLE, ExitCode: 0, Stderr: "timed out"},
			wantVerdict: model.VerdictUKE,
			wantMessage: "timed out",
		},
		{
			name:        "nonzero protocol exit",
			runResult:   RunResult{Verdict: execution.VerdictRE, ExitCode: 3},
			wantVerdict: model.VerdictUKE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordingCheckerRunner{result: tt.runResult}
			prepared := &compiledChecker{
				runner:   runner,
				artifact: execution.Artifact{Data: []byte("binary"), Mode: 0o755},
			}

			result, err := prepared.Check(context.Background(), "input", "actual", "expected")
			require.NoError(t, err)
			assert.Equal(t, tt.wantVerdict, result.Verdict)
			assert.Equal(t, tt.wantMessage, result.Message)
		})
	}
}

func TestCompiledChecker_CheckBuildsRunRequest(t *testing.T) {
	runner := &recordingCheckerRunner{result: RunResult{Verdict: execution.VerdictOK, ExitCode: 0}}
	prepared := &compiledChecker{
		runner:   runner,
		artifact: execution.Artifact{Data: []byte("binary"), Mode: 0o755},
	}

	_, err := prepared.Check(context.Background(), "input", "actual", "expected")
	require.NoError(t, err)
	require.Len(t, runner.requests, 1)

	req := runner.requests[0]
	assert.Equal(t, "docker.io/library/debian:12-slim", req.ImageRef)
	assert.Equal(t, checkerRunLimits(), req.Limits)
	assert.Equal(t, []string{
		"./checker",
		"input.txt",
		"output.txt",
		"answer.txt",
	}, req.Command)
	require.Len(t, req.Files, 4)
	assert.Equal(t, []string{"checker", "input.txt", "output.txt", "answer.txt"}, []string{
		req.Files[0].Name,
		req.Files[1].Name,
		req.Files[2].Name,
		req.Files[3].Name,
	})
	assert.Equal(t, []byte("input"), req.Files[1].Content)
	assert.Equal(t, []byte("actual"), req.Files[2].Content)
	assert.Equal(t, []byte("expected"), req.Files[3].Content)
}

func TestCompiledChecker_CheckRunnerError(t *testing.T) {
	prepared := &compiledChecker{
		runner:   &recordingCheckerRunner{err: errors.New("sandbox unavailable")},
		artifact: execution.Artifact{Data: []byte("binary"), Mode: 0o755},
	}

	result, err := prepared.Check(context.Background(), "", "", "")

	require.ErrorContains(t, err, "sandbox unavailable")
	assert.Equal(t, model.VerdictUKE, result.Verdict)
}

func TestCompiledChecker_CheckConcurrent(t *testing.T) {
	const calls = 8
	runner := &recordingCheckerRunner{result: RunResult{Verdict: execution.VerdictOK, ExitCode: 0}}
	prepared := &compiledChecker{
		runner:   runner,
		artifact: execution.Artifact{Data: []byte("binary"), Mode: 0o755},
	}

	var wg sync.WaitGroup
	errs := make([]error, calls)
	for i := range calls {
		wg.Go(func() {
			_, errs[i] = prepared.Check(t.Context(), "input", "actual", "expected")
		})
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Len(t, runner.requests, calls)
}
