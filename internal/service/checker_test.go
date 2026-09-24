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
	compileCount atomic.Int32
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

func sourceChecker(t *testing.T, checkerModule checker, choice checkerChoice) checkerSource {
	t.Helper()

	src, err := checkerModule.Source(choice)
	require.NoError(t, err)
	return src
}

func TestResolveChecker(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		inlineSource string
		wantKind     checkerKind
		wantValue    string
		wantErr      string
	}{
		{name: "empty selects default", input: "", wantKind: checkerBuiltin, wantValue: "default"},
		{name: "valid name", input: "ncmp", wantKind: checkerBuiltin, wantValue: "ncmp"},
		{name: "path rejected", input: "../ncmp.cpp", wantErr: "invalid path characters"},
		{
			name:      "valid external path",
			input:     "external:testcase-15/checker.cpp",
			wantKind:  checkerExternal,
			wantValue: "testcase-15/checker.cpp",
		},
		{
			name:         "inline source",
			inlineSource: "#include \"testlib.h\"\n",
			wantKind:     checkerInline,
			wantValue:    "#include \"testlib.h\"\n",
		},
		{
			name:         "named and inline checker conflict",
			input:        "default",
			inlineSource: "checker source",
			wantErr:      "cannot be provided together",
		},
		{name: "blank inline source", inlineSource: " \n\t", wantErr: "must not be blank"},
		{name: "path traversal rejected", input: "external:../etc/passwd", wantErr: "escapes resource root"},
		{name: "non-cpp rejected", input: "external:script.sh", wantErr: "must be a .cpp file"},
		{name: "empty path", input: "external:", wantErr: "external checker path is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			choice, err := resolveChecker(tt.input, tt.inlineSource)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantKind, choice.kind)
			assert.Equal(t, tt.wantValue, choice.value)
		})
	}
}

func TestCheckerEngine_Source(t *testing.T) {
	tests := []struct {
		name       string
		reference  string
		externalFS fs.FS
		wantErr    string
	}{
		{
			name:      "requested builtin missing",
			reference: "ncmp",
			wantErr:   `builtin checker "ncmp" is not available`,
		},
		{
			name:      "external resources not configured",
			reference: "external:custom.cpp",
			wantErr:   `external checker "custom.cpp" requires external resources`,
		},
		{
			name:       "external checker missing",
			reference:  "external:custom.cpp",
			externalFS: testFileSystem(nil),
			wantErr:    `external checker "custom.cpp" is not available`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := &checkerEngine{bundledFS: checkerTestFS(), externalFS: tt.externalFS}
			choice, err := resolveChecker(tt.reference, "")
			require.NoError(t, err)
			_, err = engine.Source(choice)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestCheckerEngine_SourceInlineSource(t *testing.T) {
	const source = "  #include \"testlib.h\"\n"

	engine := &checkerEngine{}
	plan, err := engine.Source(checkerChoice{kind: checkerInline, value: source})
	require.NoError(t, err)

	snapshot, ok := plan.(*checkerSnapshot)
	require.True(t, ok)
	assert.Equal(t, source, string(snapshot.source))
}

func TestCheckerPlan_PrepareCachesOnlySuccessfulCompilations(t *testing.T) {
	tests := []struct {
		name             string
		output           execution.CompileResult
		compileErr       error
		wantSucceeded    bool
		wantLog          string
		wantErr          string
		wantCompileCalls int
	}{
		{
			name:             "successful compilation",
			output:           successfulCheckerCompile(),
			wantSucceeded:    true,
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
			wantLog:          "syntax error",
			wantCompileCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &checkerExecutorFake{compileResult: tt.output, compileErr: tt.compileErr}
			checkerModule := newUnitChecker(t, executor, nil)

			for range 2 {
				plan := sourceChecker(t, checkerModule, checkerChoice{
					kind:  checkerBuiltin,
					value: defaultCheckerName,
				})
				preparation, err := plan.Compile(t.Context())
				if tt.wantErr != "" {
					require.ErrorContains(t, err, tt.wantErr)
					assert.Nil(t, preparation.checker)
					continue
				}
				require.NoError(t, err)
				assert.Equal(t, tt.wantSucceeded, preparation.compile.Succeeded)
				assert.Equal(t, tt.wantLog, preparation.compile.Log)
				if tt.wantSucceeded {
					assert.NotNil(t, preparation.checker)
				} else {
					assert.Nil(t, preparation.checker)
				}
			}

			assert.Len(t, executor.compileRequests, tt.wantCompileCalls)
		})
	}
}

func TestCheckerPlan_PreparePreservesDiagnosticsInCache(t *testing.T) {
	const warning = "warning: unused parameter\n"
	output := successfulCheckerCompile()
	output.Log = warning
	executor := &checkerExecutorFake{compileResult: output}
	checkerModule := newUnitChecker(t, executor, nil)
	for range 2 {
		plan := sourceChecker(t, checkerModule, checkerChoice{
			kind:  checkerBuiltin,
			value: defaultCheckerName,
		})
		preparation, err := plan.Compile(t.Context())
		require.NoError(t, err)
		assert.True(t, preparation.compile.Succeeded)
		assert.Equal(t, warning, preparation.compile.Log)
	}
	assert.Len(t, executor.compileRequests, 1)
}

func TestCheckerPlan_PrepareCallerCancellationDoesNotCancelSharedCompilation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		started := make(chan context.Context, 1)
		executor := &gatedCheckerExecutor{
			release: release,
			started: started,
		}
		checkerModule := newUnitChecker(t, executor, nil)
		choice := checkerChoice{kind: checkerBuiltin, value: defaultCheckerName}
		firstPlan := sourceChecker(t, checkerModule, choice)
		secondPlan := sourceChecker(t, checkerModule, choice)

		ctx, cancel := context.WithCancel(t.Context())
		firstResult := make(chan error, 1)
		go func() {
			_, err := firstPlan.Compile(ctx)
			firstResult <- err
		}()

		compileCtx := <-started
		secondResult := make(chan error, 1)
		go func() {
			_, err := secondPlan.Compile(t.Context())
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
		assert.Equal(t, int32(1), executor.compileCount.Load())
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
	choice := checkerChoice{kind: checkerExternal, value: "custom.cpp"}
	originalPlan := sourceChecker(t, checkerModule, choice)
	externalFS["custom.cpp"] = &fstest.MapFile{Data: []byte(updatedSource)}
	updatedPlan := sourceChecker(t, checkerModule, choice)

	delete(externalFS, "custom.cpp")

	_, err := originalPlan.Compile(t.Context())
	require.NoError(t, err)
	_, err = updatedPlan.Compile(t.Context())
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
		wantOutcome checkerOutcome
		wantMessage string
	}{
		{
			name:        "accepted with stderr message",
			runResult:   execution.RunResult{Verdict: execution.VerdictOK, ExitCode: 0, Stderr: " accepted "},
			wantOutcome: checkerAccepted,
			wantMessage: "accepted",
		},
		{
			name:        "wrong answer exit one",
			runResult:   execution.RunResult{Verdict: execution.VerdictRE, ExitCode: 1, Stdout: "wrong"},
			wantOutcome: checkerRejected,
			wantMessage: "wrong",
		},
		{
			name:        "wrong answer exit two",
			runResult:   execution.RunResult{Verdict: execution.VerdictRE, ExitCode: 2, ExtraInfo: "presentation"},
			wantOutcome: checkerRejected,
			wantMessage: "presentation",
		},
		{
			name:        "sandbox timeout",
			runResult:   execution.RunResult{Verdict: execution.VerdictTLE, ExitCode: 0, Stderr: "timed out"},
			wantOutcome: checkerFailed,
			wantMessage: "timed out",
		},
		{
			name:        "nonzero protocol exit",
			runResult:   execution.RunResult{Verdict: execution.VerdictRE, ExitCode: 3},
			wantOutcome: checkerFailed,
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
			assert.Equal(t, tt.wantOutcome, result.Outcome)
			assert.Equal(t, tt.wantMessage, result.Message)
		})
	}
}
