package service

import (
	"context"
	"errors"
	"io/fs"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"testing/synctest"

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

type gatedCheckerExecutor struct {
	release      chan struct{}
	started      chan context.Context
	compileCount *atomic.Int32
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

func (e *gatedCheckerExecutor) Compile(
	ctx context.Context,
	_ execution.CompileRequest,
) (execution.CompileResult, error) {
	if e.started != nil {
		e.started <- ctx
	}
	<-e.release
	e.compileCount.Add(1)
	return successfulCheckerCompile(), nil
}

func (e *gatedCheckerExecutor) Run(
	context.Context,
	execution.RunRequest,
) (execution.RunResult, error) {
	return execution.RunResult{}, nil
}

func successfulCheckerCompile() execution.CompileResult {
	return execution.CompileResult{Artifact: &execution.Artifact{
		Name: checkerArtifactName,
		Data: []byte("checker binary"),
		Mode: 0o755,
	}}
}

func checkerTestFS() fstest.MapFS {
	return testFileSystem(map[string][]byte{
		"checkers/default.cpp": []byte("checker source"),
		testlibHeaderKey:       []byte("testlib header"),
	})
}

func newUnitChecker(t *testing.T, executor execution.Executor, externalFS fs.FS) checker {
	t.Helper()

	checkerModule, err := newChecker(executor, checkerTestFS(), externalFS)
	require.NoError(t, err)
	return checkerModule
}

func materializeCheckerPlan(t *testing.T, checkerModule checker, location checkerLocation) checkerPlan {
	t.Helper()

	plan, err := checkerModule.Materialize(location)
	require.NoError(t, err)
	return plan
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

func TestCheckerPlan_PrepareCachesOnlySuccessfulCompilations(t *testing.T) {
	tests := []struct {
		name             string
		output           execution.CompileResult
		compileErr       error
		wantErr          string
		wantCompileCalls int
	}{
		{
			name:             "successful compilation",
			output:           successfulCheckerCompile(),
			wantCompileCalls: 1,
		},
		{
			name:             "compiler infrastructure error",
			compileErr:       errors.New("compiler unavailable"),
			wantErr:          "checker setup failed: compiler unavailable",
			wantCompileCalls: 2,
		},
		{
			name:             "compilation failed",
			output:           execution.CompileResult{Log: "syntax error"},
			wantErr:          "checker compilation failed: syntax error",
			wantCompileCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &checkerExecutorFake{compileResult: tt.output, compileErr: tt.compileErr}
			checkerModule := newUnitChecker(t, executor, nil)

			for range 2 {
				plan := materializeCheckerPlan(t, checkerModule, checkerLocation{path: defaultCheckerName})
				prepared, err := plan.Prepare(t.Context())
				if tt.wantErr != "" {
					require.ErrorContains(t, err, tt.wantErr)
					assert.Nil(t, prepared)
					continue
				}
				require.NoError(t, err)
				assert.NotNil(t, prepared)
			}

			assert.Len(t, executor.compileRequests, tt.wantCompileCalls)
		})
	}
}

func TestCheckerPlan_PrepareCoalescesConcurrentCompilations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var compileCount atomic.Int32
		release := make(chan struct{})
		executor := &gatedCheckerExecutor{release: release, compileCount: &compileCount}
		checkerModule := newUnitChecker(t, executor, nil)

		const callers = 5
		plans := make([]checkerPlan, callers)
		for i := range plans {
			plans[i] = materializeCheckerPlan(t, checkerModule, checkerLocation{path: defaultCheckerName})
		}

		errs := make([]error, callers)
		for i := range plans {
			go func() {
				_, errs[i] = plans[i].Prepare(t.Context())
			}()
		}

		synctest.Wait()
		close(release)
		synctest.Wait()

		for i, err := range errs {
			require.NoError(t, err, "caller %d", i)
		}
		assert.Equal(t, int32(1), compileCount.Load())
	})
}

func TestCheckerPlan_PrepareCallerCancellationDoesNotCancelSharedCompilation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var compileCount atomic.Int32
		release := make(chan struct{})
		started := make(chan context.Context, 1)
		executor := &gatedCheckerExecutor{
			release:      release,
			started:      started,
			compileCount: &compileCount,
		}
		checkerModule := newUnitChecker(t, executor, nil)
		firstPlan := materializeCheckerPlan(t, checkerModule, checkerLocation{path: defaultCheckerName})
		secondPlan := materializeCheckerPlan(t, checkerModule, checkerLocation{path: defaultCheckerName})

		ctx, cancel := context.WithCancel(t.Context())
		firstResult := make(chan error, 1)
		go func() {
			_, err := firstPlan.Prepare(ctx)
			firstResult <- err
		}()

		compileCtx := <-started
		secondResult := make(chan error, 1)
		go func() {
			_, err := secondPlan.Prepare(t.Context())
			secondResult <- err
		}()
		synctest.Wait()

		cancel()
		synctest.Wait()
		require.ErrorIs(t, <-firstResult, context.Canceled)
		require.NoError(t, compileCtx.Err())

		close(release)
		synctest.Wait()
		require.NoError(t, <-secondResult)
		assert.Equal(t, int32(1), compileCount.Load())
	})
}

func TestCheckerPlan_PrepareUsesCapturedSource(t *testing.T) {
	const (
		originalSource = "original source"
		updatedSource  = "updated source"
	)

	externalFS := testFileSystem(map[string][]byte{"custom.cpp": []byte(originalSource)})
	executor := &checkerExecutorFake{compileResult: successfulCheckerCompile()}
	checkerModule := newUnitChecker(t, executor, externalFS)
	location := checkerLocation{isExternal: true, path: "custom.cpp"}
	originalPlan := materializeCheckerPlan(t, checkerModule, location)
	externalFS["custom.cpp"] = &fstest.MapFile{Data: []byte(updatedSource)}
	updatedPlan := materializeCheckerPlan(t, checkerModule, location)

	delete(externalFS, "custom.cpp")

	_, err := originalPlan.Prepare(t.Context())
	require.NoError(t, err)
	_, err = updatedPlan.Prepare(t.Context())
	require.NoError(t, err)
	require.Len(t, executor.compileRequests, 2)
	require.NotEmpty(t, executor.compileRequests[0].Files)
	require.NotEmpty(t, executor.compileRequests[1].Files)
	assert.Equal(t, originalSource, string(executor.compileRequests[0].Files[0].Content))
	assert.Equal(t, updatedSource, string(executor.compileRequests[1].Files[0].Content))
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
		{
			name:        "unset sandbox verdict",
			runResult:   execution.RunResult{},
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
