package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCompiler supports sequential compile results.
// If compileResults is set, each call returns the next result in order.
// Otherwise, it returns the single artifact/result/err.
type fakeCompiler struct {
	artifact       *model.CompiledArtifact
	result         model.CompileResult
	err            error
	compileResults []CompileOutput
	calls          int
}

func (c *fakeCompiler) Compile(_ context.Context, _ CompileRequest) (CompileOutput, error) {
	if c.err != nil {
		return CompileOutput{}, c.err
	}
	if len(c.compileResults) > 0 {
		idx := c.calls
		if idx >= len(c.compileResults) {
			idx = len(c.compileResults) - 1
		}
		c.calls++
		return c.compileResults[idx], nil
	}
	c.calls++
	return CompileOutput{
		Result:   c.result,
		Artifact: c.artifact,
	}, nil
}

// fakeRunner supports sequential run results.
// For JudgeEngine tests, calls alternate: user execution, checker execution, user execution, checker execution...
type fakeRunner struct {
	preflightErr error
	runErr       error
	runResult    RunResult
	runResults   []RunResult
	calls        int
}

func (r *fakeRunner) PreflightCheck(_ context.Context) error {
	return r.preflightErr
}

func (r *fakeRunner) Run(_ context.Context, _ RunRequest) (RunResult, error) {
	if r.runErr != nil {
		return RunResult{}, r.runErr
	}
	if len(r.runResults) > 0 {
		idx := r.calls
		if idx >= len(r.runResults) {
			idx = len(r.runResults) - 1
		}
		r.calls++
		return r.runResults[idx], nil
	}
	r.calls++
	return r.runResult, nil
}

type fakeResourceStore struct {
	files map[string][]byte
	err   error
	keys  []string
}

func (s *fakeResourceStore) Get(_ context.Context, key string) ([]byte, error) {
	s.keys = append(s.keys, key)
	if s.err != nil {
		return nil, s.err
	}

	content, ok := s.files[key]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", key)
	}

	return append([]byte(nil), content...), nil
}

func testCompiledArtifact() *model.CompiledArtifact {
	return &model.CompiledArtifact{
		Name: "program",
		Data: []byte("binary"),
		Mode: 0o755,
	}
}

func testCheckerArtifact() *model.CompiledArtifact {
	return &model.CompiledArtifact{
		Name: checkerArtifactFileName,
		Data: []byte("checker-binary"),
		Mode: 0o755,
	}
}

// successCompileResults returns compile results for: 1st call = user code success, 2nd call = checker success.
func successCompileResults() []CompileOutput {
	return []CompileOutput{
		{Result: model.CompileResult{Succeeded: true}, Artifact: testCompiledArtifact()},
		{Result: model.CompileResult{Succeeded: true}, Artifact: testCheckerArtifact()},
	}
}

// userOKRunResult returns a RunResult simulating successful user code execution with given stdout.
func userOKRunResult(stdout string) RunResult {
	return RunResult{
		ExitCode: 0,
		Stdout:   stdout,
		Verdict:  sandbox.VerdictOK,
	}
}

// checkerOKRunResult returns a RunResult simulating checker exit code 0 (accepted).
func checkerOKRunResult() RunResult {
	return RunResult{
		ExitCode: 0,
		Verdict:  sandbox.VerdictOK,
		Stderr:   "ok",
	}
}

// checkerWARunResult returns a RunResult simulating checker exit code 1 (wrong answer).
func checkerWARunResult(message string) RunResult {
	return RunResult{
		ExitCode: 1,
		Verdict:  sandbox.VerdictOK,
		Stderr:   message,
	}
}

func newTestJudgeEngine(
	runner *fakeRunner,
	compiler *fakeCompiler,
	resources *fakeResourceStore,
) *JudgeEngine {
	if runner == nil {
		runner = &fakeRunner{}
	}
	if compiler == nil {
		compiler = &fakeCompiler{
			compileResults: successCompileResults(),
		}
	}
	if resources == nil {
		resources = &fakeResourceStore{files: map[string][]byte{
			defaultCheckerSourceKey: []byte("checker source"),
			testlibHeaderKey:        []byte("header"),
		}}
	}
	checkerPolicy, err := NewCheckerPolicy(defaultCheckerName, BuiltinCheckerNames())
	if err != nil {
		panic(err)
	}

	return NewJudgeEngine(compiler, runner, resources, nil, checkerPolicy, nil)
}

func baseJudgeRequest(testCases ...model.JudgeTestCase) model.JudgeRequest {
	if len(testCases) == 0 {
		testCases = []model.JudgeTestCase{{Name: "case1", InputText: "", ExpectedOutput: ""}}
	}
	return model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases:   testCases,
	}
}

func TestJudgeEngine_CompileError(t *testing.T) {
	compiler := &fakeCompiler{
		compileResults: []CompileOutput{
			// User code compile fails — checker compile should never be called
			{Result: model.CompileResult{Succeeded: false, Log: "compile failed"}},
		},
	}
	engine := newTestJudgeEngine(nil, compiler, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest())

	assert.Equal(t, model.VerdictCE, result.Verdict)
	assert.False(t, result.Compile.Succeeded)
	assert.Equal(t, "compile failed", result.Compile.Log)
	assert.Empty(t, result.Cases)
	assert.Equal(t, 1, compiler.calls, "only user code compile should be called")
	assert.Equal(t, 1, result.TotalCount)
}

func TestJudgeEngine_WrongAnswerAfterOK(t *testing.T) {
	runner := &fakeRunner{runResults: []RunResult{
		userOKRunResult("41\n"), // user execution
		checkerWARunResult("1st lines differ - expected: '42', found: '41'"), // checker
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "case1", InputText: "", ExpectedOutput: "42\n"},
	))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictWA, result.Cases[0].Verdict)
	assert.Equal(t, "1st lines differ - expected: '42', found: '41'", result.Cases[0].ExtraInfo)
	assert.Equal(t, model.VerdictWA, result.Verdict)
	assert.Equal(t, 0, result.PassedCount)
	assert.Equal(t, 2, runner.calls, "one user run + one checker run")
}

func TestJudgeEngine_CheckerInfrastructureErrorMarksOnlyCurrentCase(t *testing.T) {
	runner := &fakeRunner{runResults: []RunResult{
		userOKRunResult("2\n"), checkerOKRunResult(), // case-1: OK
		userOKRunResult("4\n"), {ExitCode: 0, Verdict: sandbox.VerdictTLE, Stderr: "checker timed out"}, // case-2: checker UKE
		userOKRunResult("6\n"), checkerOKRunResult(), // case-3: OK
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "case-1", ExpectedOutput: "2\n"},
		model.JudgeTestCase{Name: "case-2", ExpectedOutput: "4\n"},
		model.JudgeTestCase{Name: "case-3", ExpectedOutput: "6\n"},
	))

	require.Len(t, result.Cases, 3)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)
	assert.Equal(t, model.VerdictUKE, result.Cases[1].Verdict)
	assert.Contains(t, result.Cases[1].ExtraInfo, "checker timed out")
	assert.Equal(t, model.VerdictOK, result.Cases[2].Verdict)
	assert.Equal(t, model.VerdictUKE, result.Verdict)
	assert.Equal(t, 2, result.PassedCount)
}

func TestJudgeEngine_AggregateRuntimePriority(t *testing.T) {
	// All user executions hit runtime errors — checker never runs
	runner := &fakeRunner{runResults: []RunResult{
		{Verdict: sandbox.VerdictRE, ExitCode: 1},
		{Verdict: sandbox.VerdictTLE, ExitCode: 124},
		{Verdict: sandbox.VerdictMLE, ExitCode: 137},
		{Verdict: sandbox.VerdictOLE, ExitCode: 1},
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "1"},
		model.JudgeTestCase{Name: "2"},
		model.JudgeTestCase{Name: "3"},
		model.JudgeTestCase{Name: "4"},
	))

	assert.Equal(t, model.VerdictOLE, result.Verdict)
	assert.Equal(t, 4, runner.calls, "only user runs, no checker runs for runtime errors")
}

func TestJudgeEngine_CompilerInfraError(t *testing.T) {
	engine := newTestJudgeEngine(nil, &fakeCompiler{err: errors.New("boom")}, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest())

	assert.Equal(t, model.VerdictUKE, result.Verdict)
	assert.False(t, result.Compile.Succeeded)
	assert.Contains(t, result.Compile.Log, "compile infrastructure error")
}

func TestJudgeEngine_MultipleTestCases_MixedResults(t *testing.T) {
	runner := &fakeRunner{runResults: []RunResult{
		userOKRunResult("2\n"), checkerOKRunResult(), // case-1: OK
		userOKRunResult("4\n"), checkerWARunResult("2nd lines differ"), // case-2: WA
		{Verdict: sandbox.VerdictTLE, ExitCode: 124}, // case-3: TLE (no checker)
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "case-1", InputText: "1\n", ExpectedOutput: "2\n"},
		model.JudgeTestCase{Name: "case-2", InputText: "2\n", ExpectedOutput: "8\n"},
		model.JudgeTestCase{Name: "case-3", InputText: "3\n", ExpectedOutput: "6\n"},
	))

	require.Len(t, result.Cases, 3)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)
	assert.Equal(t, model.VerdictWA, result.Cases[1].Verdict)
	assert.Equal(t, "2nd lines differ", result.Cases[1].ExtraInfo)
	assert.Equal(t, model.VerdictTLE, result.Cases[2].Verdict)
	assert.Equal(t, model.VerdictTLE, result.Verdict)
	assert.Equal(t, 1, result.PassedCount)
}

func TestJudgeEngine_AllTestCasesPass(t *testing.T) {
	runner := &fakeRunner{runResults: []RunResult{
		userOKRunResult("2\n"), checkerOKRunResult(),
		userOKRunResult("4\n"), checkerOKRunResult(),
		userOKRunResult("6\n"), checkerOKRunResult(),
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "case-1", InputText: "1\n", ExpectedOutput: "2\n"},
		model.JudgeTestCase{Name: "case-2", InputText: "2\n", ExpectedOutput: "4\n"},
		model.JudgeTestCase{Name: "case-3", InputText: "3\n", ExpectedOutput: "6\n"},
	))

	assert.Equal(t, model.VerdictOK, result.Verdict)
	assert.Equal(t, 3, result.PassedCount)
	assert.Equal(t, 3, result.TotalCount)
}

func TestJudgeEngine_EmptyTestCases(t *testing.T) {
	engine := newTestJudgeEngine(nil, nil, nil)

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases:   []model.JudgeTestCase{},
	})

	assert.Equal(t, model.VerdictUKE, result.Verdict)
	assert.False(t, result.Compile.Succeeded)
	assert.Contains(t, result.Compile.Log, "at least one testcase is required")
}

func TestJudgeEngine_SingleTestCase(t *testing.T) {
	runner := &fakeRunner{runResults: []RunResult{
		userOKRunResult("42\n"), checkerOKRunResult(),
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "print(42)",
		Language:    model.LanguagePython,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "only-case", InputText: "", ExpectedOutput: "42\n"},
		},
	})

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)
	assert.Equal(t, model.VerdictOK, result.Verdict)
	assert.Equal(t, 1, result.PassedCount)
}

func TestJudgeEngine_InvalidRequest(t *testing.T) {
	engine := newTestJudgeEngine(nil, nil, nil)

	tests := []struct {
		name   string
		req    model.JudgeRequest
		errMsg string
	}{
		{
			name: "empty source code",
			req: model.JudgeRequest{
				SourceCode:  "",
				Language:    model.LanguagePython,
				TimeLimit:   1000,
				MemoryLimit: 128,
				TestCases:   []model.JudgeTestCase{{Name: "case1"}},
			},
			errMsg: "source code is required",
		},
		{
			name: "unknown language",
			req: model.JudgeRequest{
				SourceCode:  "code",
				Language:    model.LanguageUnknown,
				TimeLimit:   1000,
				MemoryLimit: 128,
				TestCases:   []model.JudgeTestCase{{Name: "case1"}},
			},
			errMsg: "language is required",
		},
		{
			name: "zero time limit",
			req: model.JudgeRequest{
				SourceCode:  "code",
				Language:    model.LanguagePython,
				TimeLimit:   0,
				MemoryLimit: 128,
				TestCases:   []model.JudgeTestCase{{Name: "case1"}},
			},
			errMsg: "time limit must be positive",
		},
		{
			name: "negative memory limit",
			req: model.JudgeRequest{
				SourceCode:  "code",
				Language:    model.LanguagePython,
				TimeLimit:   1000,
				MemoryLimit: -1,
				TestCases:   []model.JudgeTestCase{{Name: "case1"}},
			},
			errMsg: "memory limit must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.Judge(context.Background(), tt.req)
			assert.Equal(t, model.VerdictUKE, result.Verdict)
			assert.False(t, result.Compile.Succeeded)
			assert.Contains(t, result.Compile.Log, tt.errMsg)
		})
	}
}

func TestJudgeEngine_CheckerCompileFailureReturnsUnknownError(t *testing.T) {
	compiler := &fakeCompiler{compileResults: []CompileOutput{
		{Result: model.CompileResult{Succeeded: true}, Artifact: testCompiledArtifact()},
		{Result: model.CompileResult{Succeeded: false, Log: "fatal error: testlib.h missing"}},
	}}
	engine := newTestJudgeEngine(nil, compiler, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "case-1"},
		model.JudgeTestCase{Name: "case-2"},
	))

	assert.Equal(t, model.VerdictUKE, result.Verdict)
	assert.True(t, result.Compile.Succeeded)
	require.Len(t, result.Cases, 2)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "checker compilation failed")
}

func TestJudgeEngine_MissingCheckerResourceReturnsUnknownError(t *testing.T) {
	// Only checker source, no testlib.h — prepareChecker will fail loading testlib.h
	resources := &fakeResourceStore{files: map[string][]byte{
		defaultCheckerSourceKey: []byte("checker source"),
	}}
	engine := newTestJudgeEngine(nil, nil, resources)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "case-1"},
	))

	assert.Equal(t, model.VerdictUKE, result.Verdict)
	assert.True(t, result.Compile.Succeeded)
	require.Len(t, result.Cases, 1)
	assert.Contains(t, result.Cases[0].ExtraInfo, testlibHeaderKey)
}

func TestJudgeEngine_ValidateCheckerPolicy_RejectsDisallowedChecker(t *testing.T) {
	checkerPolicy, err := NewCheckerPolicy(defaultCheckerName, []string{defaultCheckerName})
	require.NoError(t, err)

	engine := NewJudgeEngine(
		&fakeCompiler{},
		&fakeRunner{},
		&fakeResourceStore{},
		nil,
		checkerPolicy,
		nil,
	)

	err = engine.ValidateCheckerPolicy(context.Background(), model.JudgeRequest{Checker: "ncmp"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `checker "ncmp" is not allowed`)
}

func TestJudgeEngine_Judge_UsesRequestedChecker(t *testing.T) {
	resources := &fakeResourceStore{files: map[string][]byte{
		defaultCheckerSourceKey: []byte("default checker source"),
		"checkers/yesno.cpp":    []byte("yesno checker source"),
		testlibHeaderKey:        []byte("header"),
	}}
	runner := &fakeRunner{runResults: []RunResult{
		userOKRunResult("YES\n"), checkerOKRunResult(),
	}}
	engine := newTestJudgeEngine(runner, nil, resources)

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Checker:     "yesno",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "case-1", ExpectedOutput: "YES\n"},
		},
	})

	assert.Equal(t, model.VerdictOK, result.Verdict)
	assert.Contains(t, resources.keys, "checkers/yesno.cpp")
	assert.NotContains(t, resources.keys, defaultCheckerSourceKey)
}

func TestJudgeEngine_UserRuntimeErrorSkipsChecker(t *testing.T) {
	runner := &fakeRunner{runResults: []RunResult{
		{Verdict: sandbox.VerdictTLE, ExitCode: 124}, // user TLE — no checker call
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "case-1"},
	))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictTLE, result.Cases[0].Verdict)
	assert.Equal(t, 1, runner.calls, "only user run, no checker run")
}

func TestJudgeEngine_CheckerRunnerErrorMarksCaseUnknownError(t *testing.T) {
	customRunner := &sequentialErrRunner{
		results: []runCallResult{
			{result: userOKRunResult("42\n")},
			{err: errors.New("sandbox boom")},
			{result: userOKRunResult("42\n")},
			{err: errors.New("sandbox boom")},
		},
	}
	engine := newTestJudgeEngine(nil, nil, nil)
	engine.runner = customRunner

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{Name: "case-1", ExpectedOutput: "42\n"},
		model.JudgeTestCase{Name: "case-2", ExpectedOutput: "42\n"},
	))

	require.Len(t, result.Cases, 2)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "checker infrastructure error")
	assert.Equal(t, model.VerdictUKE, result.Verdict)
	assert.Equal(t, 4, customRunner.calls)
}

// sequentialErrRunner allows per-call error control.
type runCallResult struct {
	result RunResult
	err    error
}

type sequentialErrRunner struct {
	results []runCallResult
	calls   int
}

func (r *sequentialErrRunner) PreflightCheck(_ context.Context) error { return nil }

func (r *sequentialErrRunner) Run(_ context.Context, _ RunRequest) (RunResult, error) {
	idx := r.calls
	if idx >= len(r.results) {
		idx = len(r.results) - 1
	}
	r.calls++
	return r.results[idx].result, r.results[idx].err
}

func TestJudgeEngine_DoesNotMutateCallerRequest(t *testing.T) {
	fakeStorage := &fakeExternalStorage{
		files: map[string][]byte{
			"test.in":  []byte("input data"),
			"test.out": []byte("expected output"),
		},
	}

	runner := &fakeRunner{runResults: []RunResult{
		userOKRunResult("expected output"), checkerOKRunResult(),
		userOKRunResult("expected output"), checkerOKRunResult(),
	}}
	compiler := &fakeCompiler{compileResults: successCompileResults()}
	resources := &fakeResourceStore{files: map[string][]byte{
		defaultCheckerSourceKey: []byte("checker source"),
		testlibHeaderKey:        []byte("header"),
	}}

	checkerPolicy, err := NewCheckerPolicy("default", BuiltinCheckerNames())
	require.NoError(t, err)

	engine := &JudgeEngine{
		compiler:        compiler,
		runner:          runner,
		resources:       resources,
		externalStorage: fakeStorage,
		checkerPolicy:   checkerPolicy,
		log:             slog.Default(),
	}

	originalReq := model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{
				Name:               "case-1",
				InputFile:          "test.in",
				ExpectedOutputFile: "test.out",
			},
		},
	}

	result := engine.Judge(context.Background(), originalReq)
	assert.Equal(t, model.VerdictOK, result.Verdict)

	// Verify original request was NOT mutated
	assert.Equal(t, "test.in", originalReq.TestCases[0].InputFile, "InputFile should not be cleared")
	assert.Equal(t, "test.out", originalReq.TestCases[0].ExpectedOutputFile, "ExpectedOutputFile should not be cleared")
	assert.Empty(t, originalReq.TestCases[0].InputText, "InputText should remain empty")
	assert.Empty(t, originalReq.TestCases[0].ExpectedOutput, "ExpectedOutput should remain empty")

	// Call Judge again with the same request to verify it still works
	result2 := engine.Judge(context.Background(), originalReq)
	assert.Equal(t, model.VerdictOK, result2.Verdict, "Second call should also succeed")
	assert.Equal(t, 4, fakeStorage.getCalls, "Should load files 4 times (2 files x 2 calls)")
}

// fakeExternalStorage implements a simple in-memory external storage for testing.
type fakeExternalStorage struct {
	files    map[string][]byte
	getCalls int
}

func (f *fakeExternalStorage) Get(_ context.Context, path string) ([]byte, error) {
	f.getCalls++
	data, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return data, nil
}
