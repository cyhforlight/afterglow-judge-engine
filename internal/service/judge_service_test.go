package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

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

	idx := c.calls
	c.calls++
	if c.err != nil {
		return CompileOutput{}, c.err
	}
	if len(c.compileResults) > 0 {
		if idx >= len(c.compileResults) {
			idx = len(c.compileResults) - 1
		}
		return c.compileResults[idx], nil
	}
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
		Verdict:  execution.VerdictOK,
	}
}

// checkerOKRunResult returns a RunResult simulating checker exit code 0 (accepted).
func checkerOKRunResult() RunResult {
	return RunResult{
		ExitCode: 0,
		Verdict:  execution.VerdictOK,
		Stderr:   "ok",
	}
}

// checkerWARunResult returns a RunResult simulating checker exit code 1 (wrong answer).
func checkerWARunResult(message string) RunResult {
	return RunResult{
		ExitCode: 1,
		Verdict:  execution.VerdictOK,
		Stderr:   message,
	}
}

func newTestJudgeEngine(
	runner *fakeRunner,
	compiler *fakeCompiler,
	resources *fakeResourceStore,
) *JudgeEngine {
	return newTestJudgeEngineWithExternalResources(runner, compiler, resources, nil)
}

func newTestJudgeEngineWithExternalResources(
	runner *fakeRunner,
	compiler *fakeCompiler,
	resources *fakeResourceStore,
	externalResources ResourceStore,
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
	engine := NewJudgeEngine(compiler, compiler, runner, resources, externalResources, 10, model.DefaultJudgeLimits())
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
		userResults: map[string]runCallResult{
			"1\n": {result: userOKRunResult("2\n")},
			"2\n": {result: userOKRunResult("4\n")},
			"3\n": {result: userOKRunResult("6\n")},
		},
		checkerResults: map[string]runCallResult{
			"2\n": {result: checkerOKRunResult()},
			"4\n": {result: RunResult{ExitCode: 0, Verdict: execution.VerdictTLE, Stderr: "checker timed out"}},
			"6\n": {result: checkerOKRunResult()},
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

func TestJudgeEngine_CompilerInfraError(t *testing.T) {
	engine := newTestJudgeEngine(nil, &fakeCompiler{err: errors.New("boom")}, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest())

	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.False(t, result.Compile.Succeeded)
	assert.Contains(t, result.Compile.Log, "compile infrastructure error")
}

func TestJudgeEngine_MultipleTestCases_MixedResults(t *testing.T) {
	runner := &inputKeyedRunner{
		userResults: map[string]runCallResult{
			"1\n": {result: userOKRunResult("2\n")},
			"2\n": {result: userOKRunResult("4\n")},
			"3\n": {result: RunResult{Verdict: execution.VerdictTLE, ExitCode: 124}},
		},
		checkerResults: map[string]runCallResult{
			"2\n": {result: checkerOKRunResult()},
			"4\n": {result: checkerWARunResult("2nd lines differ")},
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
		userResults: map[string]runCallResult{
			"1\n": {result: userOKRunResult("2\n")},
			"2\n": {result: userOKRunResult("4\n")},
			"3\n": {result: userOKRunResult("6\n")},
		},
		checkerResults: map[string]runCallResult{
			"2\n": {result: checkerOKRunResult()},
			"4\n": {result: checkerOKRunResult()},
			"6\n": {result: checkerOKRunResult()},
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
		name              string
		req               model.JudgeRequest
		resources         *fakeResourceStore
		externalResources ResourceStore
		wantErr           string
	}{
		{
			name: "invalid checker name",
			req: func() model.JudgeRequest {
				req := baseJudgeRequest()
				req.Checker = "ncmp@v2"
				return req
			}(),
			wantErr: `invalid characters`,
		},
		{
			name: "external input requires resources",
			req: baseJudgeRequest(model.JudgeTestCase{
				InputFile: "cases/1.in",
			}),
			wantErr: `inputFile "cases/1.in" requires external resources`,
		},
		{
			name: "external checker requires resources",
			req: func() model.JudgeRequest {
				req := baseJudgeRequest()
				req.Checker = "external:checkers/custom.cpp"
				return req
			}(),
			wantErr: `external checker "checkers/custom.cpp" requires external resources`,
		},
		{
			name: "missing external input file",
			req: baseJudgeRequest(model.JudgeTestCase{
				InputFile: "cases/1.in",
			}),
			externalResources: &fakeExternalResources{files: map[string][]byte{}},
			wantErr:           `testcases[0]: inputFile "cases/1.in" is not available`,
		},
		{
			name: "missing external checker file",
			req: func() model.JudgeRequest {
				req := baseJudgeRequest()
				req.Checker = "external:checkers/custom.cpp"
				return req
			}(),
			externalResources: &fakeExternalResources{files: map[string][]byte{}},
			wantErr:           `external checker "checkers/custom.cpp" is not available`,
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
			req: func() model.JudgeRequest {
				req := baseJudgeRequest()
				req.Checker = "external:checkers/custom.cpp"
				return req
			}(),
			resources: &fakeResourceStore{files: map[string][]byte{}},
			externalResources: &fakeExternalResources{files: map[string][]byte{
				"checkers/custom.cpp": []byte("checker source"),
			}},
			wantErr: `checker dependency "testlib.h" is not available`,
		},
		{
			name: "request over configured time limit",
			req: func() model.JudgeRequest {
				req := baseJudgeRequest()
				req.TimeLimit = model.DefaultJudgeLimits().MaxTimeLimitMs + 1
				return req
			}(),
			wantErr: `timeLimit must be at most`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := newTestJudgeEngine(nil, nil, tt.resources)
			engine.externalResources = tt.externalResources

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
		{Verdict: execution.VerdictTLE, ExitCode: 124}, // user TLE — no checker call
	}}
	engine := newTestJudgeEngine(runner, nil, nil)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{},
	))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictTLE, result.Cases[0].Verdict)
	assert.Equal(t, 1, runner.calls, "only user run, no checker run")
}

func TestNormalizeUserRunResult_JavaOutOfMemory(t *testing.T) {
	tests := []struct {
		name        string
		language    model.Language
		verdict     execution.Verdict
		stderr      string
		wantVerdict execution.Verdict
	}{
		{
			name:        "Java out of memory becomes MLE",
			language:    model.LanguageJava,
			verdict:     execution.VerdictRE,
			stderr:      "Exception in thread \"main\" java.lang.OutOfMemoryError: Java heap space",
			wantVerdict: execution.VerdictMLE,
		},
		{
			name:        "ordinary Java exception stays RE",
			language:    model.LanguageJava,
			verdict:     execution.VerdictRE,
			stderr:      "Exception in thread \"main\" java.lang.NullPointerException",
			wantVerdict: execution.VerdictRE,
		},
		{
			name:        "other languages are unchanged",
			language:    model.LanguageCPP,
			verdict:     execution.VerdictRE,
			stderr:      "java.lang.OutOfMemoryError",
			wantVerdict: execution.VerdictRE,
		},
		{
			name:        "existing Java MLE stays MLE",
			language:    model.LanguageJava,
			verdict:     execution.VerdictMLE,
			wantVerdict: execution.VerdictMLE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeUserRunResult(tt.language, RunResult{
				Verdict: tt.verdict,
				Stderr:  tt.stderr,
			})
			assert.Equal(t, tt.wantVerdict, got.Verdict)
		})
	}
}

func TestJudgeEngine_CheckerRunnerErrorMarksCaseUnknownError(t *testing.T) {
	customRunner := &inputKeyedRunner{
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

type runCallResult struct {
	result RunResult
	err    error
}

type inputKeyedRunner struct {
	mu             sync.Mutex
	userResults    map[string]runCallResult
	checkerResults map[string]runCallResult
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
			return res.result, res.err
		}
		return RunResult{}, fmt.Errorf("inputKeyedRunner: no user result for input %q", input)
	}

	if len(req.Files) >= 3 {
		if res, ok := r.checkerResults[string(req.Files[2].Content)]; ok {
			return res.result, res.err
		}
	}

	return RunResult{}, errors.New("inputKeyedRunner: no checker result found")
}

func TestJudgeEngine_DoesNotMutateCallerRequest(t *testing.T) {
	fakeResources := &fakeExternalResources{
		files: map[string][]byte{
			"test.in":  []byte("input data"),
			"test.out": []byte("expected output"),
		},
	}

	runner := &fakeRunner{runResults: []RunResult{
		userOKRunResult("expected output"), checkerOKRunResult(),
	}}
	compiler := &fakeCompiler{compileResults: successCompileResults()}

	engine := newTestJudgeEngineWithExternalResources(runner, compiler, nil, fakeResources)

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

	assert.Equal(t, "test.in", originalReq.TestCases[0].InputFile, "InputFile should not be cleared")
	assert.Equal(t, "test.out", originalReq.TestCases[0].ExpectedOutputFile, "ExpectedOutputFile should not be cleared")
	assert.Empty(t, originalReq.TestCases[0].InputText, "InputText should remain empty")
	assert.Empty(t, originalReq.TestCases[0].ExpectedOutput, "ExpectedOutput should remain empty")
}

type fakeExternalResources struct {
	mu    sync.Mutex
	files map[string][]byte
}

func (f *fakeExternalResources) Get(_ context.Context, path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	data, ok := f.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return data, nil
}

func (f *fakeExternalResources) Stat(_ context.Context, path string) error {
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
