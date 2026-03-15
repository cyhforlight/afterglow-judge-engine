package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCompiler supports sequential compile results.
// If compileResults is set, each call returns the next result in order.
// Otherwise, it returns the single artifact/result/err.
type fakeCompiler struct {
	mu             sync.Mutex
	artifact       *model.CompiledArtifact
	result         model.CompileResult
	err            error
	compileResults []CompileOutput
	calls          int
}

func (c *fakeCompiler) Compile(_ context.Context, _ CompileRequest) (CompileOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

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
	mu           sync.Mutex
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
	r.mu.Lock()
	defer r.mu.Unlock()

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
	mu    sync.Mutex
	files map[string][]byte
	err   error
	keys  []string
}

func (s *fakeResourceStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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

func (s *fakeResourceStore) Stat(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.keys = append(s.keys, key)
	if s.err != nil {
		return s.err
	}
	if _, ok := s.files[key]; !ok {
		return fmt.Errorf("resource not found: %s", key)
	}
	return nil
}

func testCompiledArtifact() *model.CompiledArtifact {
	return &model.CompiledArtifact{
		Data: []byte("binary"),
		Mode: 0o755,
	}
}

func testCheckerArtifact() *model.CompiledArtifact {
	return &model.CompiledArtifact{
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
	return newTestJudgeEngineWithStorage(runner, compiler, resources, nil)
}

func newTestJudgeEngineWithStorage(
	runner *fakeRunner,
	compiler *fakeCompiler,
	resources *fakeResourceStore,
	externalStorage ResourceStore,
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
			"checkers/default.cpp": []byte("checker source"),
			testlibHeaderKey:       []byte("header"),
		}}
	}
	engine := NewJudgeEngine(compiler, compiler, runner, resources, externalStorage, 10)
	return engine
}

func baseJudgeRequest(testCases ...model.JudgeTestCase) model.JudgeRequest {
	if len(testCases) == 0 {
		testCases = []model.JudgeTestCase{{InputText: "", ExpectedOutput: ""}}
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

	assert.Equal(t, model.JudgeStatusCompileError, result.Status)
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
		model.JudgeTestCase{InputText: "", ExpectedOutput: "42\n"},
	))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictWA, result.Cases[0].Verdict)
	assert.Equal(t, "1st lines differ - expected: '42', found: '41'", result.Cases[0].ExtraInfo)
	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, 0, result.PassedCount)
	assert.Equal(t, 2, runner.calls, "one user run + one checker run")
}

func TestJudgeEngine_CheckerInfrastructureErrorMarksOnlyCurrentCase(t *testing.T) {
	runner := &inputKeyedRunner{
		userResults: map[string]RunResult{
			"1\n": userOKRunResult("2\n"),
			"2\n": userOKRunResult("4\n"),
			"3\n": userOKRunResult("6\n"),
		},
		checkerResults: map[string]RunResult{
			"2\n": checkerOKRunResult(),
			"4\n": {ExitCode: 0, Verdict: sandbox.VerdictTLE, Stderr: "checker timed out"}, // checker UKE
			"6\n": checkerOKRunResult(),
		},
	}
	engine := newTestJudgeEngine(nil, nil, nil)
	engine.runner = runner

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "2\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "4\n"},
		model.JudgeTestCase{InputText: "3\n", ExpectedOutput: "6\n"},
	))

	require.Len(t, result.Cases, 3)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)
	assert.Equal(t, model.VerdictUKE, result.Cases[1].Verdict)
	assert.Contains(t, result.Cases[1].ExtraInfo, "checker timed out")
	assert.Equal(t, model.VerdictOK, result.Cases[2].Verdict)
	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.Equal(t, 2, result.PassedCount)
}

func TestJudgeEngine_AggregateRuntimePriority(t *testing.T) {
	// All user executions hit runtime errors — checker never runs.
	// With parallel execution, we can't predict which case gets which error,
	// but the test only checks aggregate verdict and total calls.
	runner := &fakeRunner{runResult: RunResult{Verdict: sandbox.VerdictRE, ExitCode: 1}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{},
		model.JudgeTestCase{},
		model.JudgeTestCase{},
		model.JudgeTestCase{},
	))

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, 4, runner.calls, "only user runs, no checker runs for runtime errors")
}

func TestJudgeEngine_CompilerInfraError(t *testing.T) {
	engine := newTestJudgeEngine(nil, &fakeCompiler{err: errors.New("boom")}, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest())

	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.False(t, result.Compile.Succeeded)
	assert.Contains(t, result.Compile.Log, "compile infrastructure error")
}

func TestJudgeEngine_MultipleTestCases_MixedResults(t *testing.T) {
	runner := &inputKeyedRunner{
		userResults: map[string]RunResult{
			"1\n": userOKRunResult("2\n"),
			"2\n": userOKRunResult("4\n"),
			"3\n": {Verdict: sandbox.VerdictTLE, ExitCode: 124}, // TLE — no checker
		},
		checkerResults: map[string]RunResult{
			"2\n": checkerOKRunResult(),                   // case-1: OK
			"4\n": checkerWARunResult("2nd lines differ"), // case-2: WA
		},
	}
	engine := newTestJudgeEngine(nil, nil, nil)
	engine.runner = runner

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "2\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "8\n"},
		model.JudgeTestCase{InputText: "3\n", ExpectedOutput: "6\n"},
	))

	require.Len(t, result.Cases, 3)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)
	assert.Equal(t, model.VerdictWA, result.Cases[1].Verdict)
	assert.Equal(t, "2nd lines differ", result.Cases[1].ExtraInfo)
	assert.Equal(t, model.VerdictTLE, result.Cases[2].Verdict)
	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, 1, result.PassedCount)
}

func TestJudgeEngine_AllTestCasesPass(t *testing.T) {
	runner := &inputKeyedRunner{
		userResults: map[string]RunResult{
			"1\n": userOKRunResult("2\n"),
			"2\n": userOKRunResult("4\n"),
			"3\n": userOKRunResult("6\n"),
		},
		checkerResults: map[string]RunResult{
			"2\n": checkerOKRunResult(),
			"4\n": checkerOKRunResult(),
			"6\n": checkerOKRunResult(),
		},
	}
	engine := newTestJudgeEngine(nil, nil, nil)
	engine.runner = runner

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "2\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "4\n"},
		model.JudgeTestCase{InputText: "3\n", ExpectedOutput: "6\n"},
	))

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, 3, result.PassedCount)
	assert.Equal(t, 3, result.TotalCount)
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
			{InputText: "", ExpectedOutput: "42\n"},
		},
	})

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)
	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, 1, result.PassedCount)
}

func TestJudgeEngine_CheckerCompileFailureReturnsUnknownError(t *testing.T) {
	compiler := &fakeCompiler{compileResults: []CompileOutput{
		{Result: model.CompileResult{Succeeded: true}, Artifact: testCompiledArtifact()},
		{Result: model.CompileResult{Succeeded: false, Log: "fatal error: testlib.h missing"}},
	}}
	engine := newTestJudgeEngine(nil, compiler, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{},
		model.JudgeTestCase{},
	))

	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.True(t, result.Compile.Succeeded)
	require.Len(t, result.Cases, 2)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "checker compilation failed")
}

func TestJudgeEngine_MissingCheckerResourceReturnsUnknownError(t *testing.T) {
	// Only checker source, no testlib.h — prepareChecker will fail loading testlib.h
	resources := &fakeResourceStore{files: map[string][]byte{
		"checkers/default.cpp": []byte("checker source"),
	}}
	engine := newTestJudgeEngine(nil, nil, resources)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{},
	))

	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.True(t, result.Compile.Succeeded)
	require.Len(t, result.Cases, 1)
	assert.Contains(t, result.Cases[0].ExtraInfo, testlibHeaderKey)
}

func TestJudgeEngine_ValidateRequest(t *testing.T) {
	tests := []struct {
		name            string
		req             model.JudgeRequest
		resources       *fakeResourceStore
		externalStorage ResourceStore
		wantErr         string
	}{
		{
			name:    "invalid checker name",
			req:     model.JudgeRequest{Checker: "NCMP"},
			wantErr: `checker "NCMP" must be a builtin short name`,
		},
		{
			name: "external input requires storage",
			req: model.JudgeRequest{
				Checker: "default",
				TestCases: []model.JudgeTestCase{{
					InputFile: "cases/1.in",
				}},
			},
			wantErr: `inputFile "cases/1.in" requires external storage`,
		},
		{
			name: "external checker requires storage",
			req: model.JudgeRequest{
				Checker: "external:checkers/custom.cpp",
			},
			wantErr: `external checker "checkers/custom.cpp" requires external storage`,
		},
		{
			name: "missing external input file",
			req: model.JudgeRequest{
				Checker: "default",
				TestCases: []model.JudgeTestCase{{
					InputFile: "cases/1.in",
				}},
			},
			externalStorage: &fakeExternalStorage{files: map[string][]byte{}},
			wantErr:         `testcases[0]: inputFile "cases/1.in" is not available`,
		},
		{
			name: "missing external checker file",
			req: model.JudgeRequest{
				Checker: "external:checkers/custom.cpp",
			},
			externalStorage: &fakeExternalStorage{files: map[string][]byte{}},
			wantErr:         `external checker "checkers/custom.cpp" is not available`,
		},
		{
			name: "missing builtin checker dependency",
			req:  baseJudgeRequest(),
			resources: &fakeResourceStore{files: map[string][]byte{
				"checkers/default.cpp": []byte("checker source"),
			}},
			wantErr: `checker dependency "testlib.h" is not available`,
		},
		{
			name: "missing external checker dependency",
			req: model.JudgeRequest{
				Checker: "external:checkers/custom.cpp",
			},
			resources: &fakeResourceStore{files: map[string][]byte{}},
			externalStorage: &fakeExternalStorage{files: map[string][]byte{
				"checkers/custom.cpp": []byte("checker source"),
			}},
			wantErr: `checker dependency "testlib.h" is not available`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := newTestJudgeEngine(nil, nil, tt.resources)
			engine.externalStorage = tt.externalStorage

			err := engine.ValidateRequest(context.Background(), tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestJudgeEngine_Judge_UsesRequestedChecker(t *testing.T) {
	resources := &fakeResourceStore{files: map[string][]byte{
		"checkers/default.cpp": []byte("default checker source"),
		"checkers/yesno.cpp":   []byte("yesno checker source"),
		testlibHeaderKey:       []byte("header"),
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
			{ExpectedOutput: "YES\n"},
		},
	})

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Contains(t, resources.keys, "checkers/yesno.cpp")
	assert.NotContains(t, resources.keys, "checkers/default.cpp")
}

func TestJudgeEngine_UserRuntimeErrorSkipsChecker(t *testing.T) {
	runner := &fakeRunner{runResults: []RunResult{
		{Verdict: sandbox.VerdictTLE, ExitCode: 124}, // user TLE — no checker call
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{},
	))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictTLE, result.Cases[0].Verdict)
	assert.Equal(t, 1, runner.calls, "only user run, no checker run")
}

func TestJudgeEngine_CheckerRunnerErrorMarksCaseUnknownError(t *testing.T) {
	customRunner := &inputKeyedErrRunner{
		userResults: map[string]runCallResult{
			"1\n": {result: userOKRunResult("42\n")},
			"2\n": {result: userOKRunResult("42\n")},
		},
		checkerResults: map[string]runCallResult{
			"42\n": {err: errors.New("sandbox boom")},
		},
	}
	engine := newTestJudgeEngine(nil, nil, nil)
	engine.runner = customRunner

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "42\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "42\n"},
	))

	require.Len(t, result.Cases, 2)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "checker infrastructure error")
	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.Equal(t, 4, customRunner.calls)
}

// runCallResult pairs a RunResult with an optional error for keyed runners.
type runCallResult struct {
	result RunResult
	err    error
}

// inputKeyedRunner dispatches run results based on request content,
// making it safe for parallel test case execution.
//
// For user runs (Stdin != nil), it looks up results by stdin text.
// For checker runs (Stdin == nil), it looks up results by the actual output
// content found in Files[2] (the checker output file).
type inputKeyedRunner struct {
	mu sync.Mutex
	// userResults maps stdin text → RunResult for user execution
	userResults map[string]RunResult
	// checkerResults maps user stdout → RunResult for checker execution
	checkerResults map[string]RunResult
	calls          int
}

func (r *inputKeyedRunner) PreflightCheck(_ context.Context) error { return nil }

func (r *inputKeyedRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls++

	if req.Stdin != nil {
		data, _ := io.ReadAll(req.Stdin)
		input := string(data)
		if res, ok := r.userResults[input]; ok {
			return res, nil
		}
		return RunResult{}, fmt.Errorf("inputKeyedRunner: no user result for input %q", input)
	}

	// Checker call — identify by output file content (Files[2] = actual output).
	if len(req.Files) >= 3 {
		if res, ok := r.checkerResults[string(req.Files[2].Content)]; ok {
			return res, nil
		}
	}

	return RunResult{}, errors.New("inputKeyedRunner: no checker result found")
}

// inputKeyedErrRunner is like inputKeyedRunner but supports per-call errors.
type inputKeyedErrRunner struct {
	mu             sync.Mutex
	userResults    map[string]runCallResult // stdin → user run result
	checkerResults map[string]runCallResult // user stdout → checker run result
	calls          int
}

func (r *inputKeyedErrRunner) PreflightCheck(_ context.Context) error { return nil }

func (r *inputKeyedErrRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls++

	if req.Stdin != nil {
		data, _ := io.ReadAll(req.Stdin)
		input := string(data)
		if res, ok := r.userResults[input]; ok {
			return res.result, res.err
		}
		return RunResult{}, fmt.Errorf("inputKeyedErrRunner: no user result for input %q", input)
	}

	if len(req.Files) >= 3 {
		if res, ok := r.checkerResults[string(req.Files[2].Content)]; ok {
			return res.result, res.err
		}
	}

	return RunResult{}, errors.New("inputKeyedErrRunner: no checker result found")
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

	engine := newTestJudgeEngineWithStorage(runner, compiler, nil, fakeStorage)

	originalReq := model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{
				InputFile:          "test.in",
				ExpectedOutputFile: "test.out",
			},
		},
	}

	result := engine.Judge(context.Background(), originalReq)
	assert.Equal(t, model.JudgeStatusOK, result.Status)

	// Verify original request was NOT mutated
	assert.Equal(t, "test.in", originalReq.TestCases[0].InputFile, "InputFile should not be cleared")
	assert.Equal(t, "test.out", originalReq.TestCases[0].ExpectedOutputFile, "ExpectedOutputFile should not be cleared")
	assert.Empty(t, originalReq.TestCases[0].InputText, "InputText should remain empty")
	assert.Empty(t, originalReq.TestCases[0].ExpectedOutput, "ExpectedOutput should remain empty")

	// Call Judge again with the same request to verify it still works
	result2 := engine.Judge(context.Background(), originalReq)
	assert.Equal(t, model.JudgeStatusOK, result2.Status, "Second call should also succeed")
	assert.Equal(t, 4, fakeStorage.getCalls, "Should load files 4 times (2 files x 2 calls)")
}

// fakeExternalStorage implements a simple in-memory external storage for testing.
type fakeExternalStorage struct {
	mu       sync.Mutex
	files    map[string][]byte
	getCalls int
}

func (f *fakeExternalStorage) Get(_ context.Context, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.getCalls++
	data, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return data, nil
}

func (f *fakeExternalStorage) Stat(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.files[path]; !ok {
		return fmt.Errorf("file not found: %s", path)
	}
	return nil
}

func TestAggregateStatus(t *testing.T) {
	tests := []struct {
		name     string
		cases    []model.JudgeCaseResult
		expected model.JudgeStatus
	}{
		{"empty cases returns SystemError", []model.JudgeCaseResult{}, model.JudgeStatusSystemError},
		{"all OK returns OK", []model.JudgeCaseResult{{Verdict: model.VerdictOK}, {Verdict: model.VerdictOK}}, model.JudgeStatusOK},
		{"WA without UKE returns OK", []model.JudgeCaseResult{{Verdict: model.VerdictOK}, {Verdict: model.VerdictWA}}, model.JudgeStatusOK},
		{"mixed runtime errors without UKE returns OK", []model.JudgeCaseResult{
			{Verdict: model.VerdictOK}, {Verdict: model.VerdictTLE}, {Verdict: model.VerdictMLE},
			{Verdict: model.VerdictRE}, {Verdict: model.VerdictOLE}, {Verdict: model.VerdictWA},
		}, model.JudgeStatusOK},
		{"any UKE returns SystemError", []model.JudgeCaseResult{
			{Verdict: model.VerdictOK}, {Verdict: model.VerdictWA}, {Verdict: model.VerdictTLE}, {Verdict: model.VerdictUKE},
		}, model.JudgeStatusSystemError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, aggregateStatus(tt.cases))
		})
	}
}
