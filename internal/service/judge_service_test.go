package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCompiler struct {
	mu       sync.Mutex
	artifact *model.CompiledArtifact
	result   model.CompileResult
	err      error
	calls    int
}

func (c *fakeCompiler) Compile(_ context.Context, _ CompileRequest) (CompileOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++
	if c.err != nil {
		return CompileOutput{}, c.err
	}
	return CompileOutput{Result: c.result, Artifact: c.artifact}, nil
}

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
		index := r.calls
		if index >= len(r.runResults) {
			index = len(r.runResults) - 1
		}
		r.calls++
		return r.runResults[index], nil
	}
	r.calls++
	return r.runResult, nil
}

type fakeChecker struct {
	mu         sync.Mutex
	resolveErr error
	resolved   *fakeResolvedChecker
	references []string
}

func newFakeChecker() *fakeChecker {
	return &fakeChecker{resolved: &fakeResolvedChecker{
		prepared: &fakePreparedChecker{result: checkerResult{Verdict: model.VerdictOK}},
	}}
}

func (c *fakeChecker) Resolve(reference string) (resolvedChecker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.references = append(c.references, reference)
	if c.resolveErr != nil {
		return nil, c.resolveErr
	}
	return c.resolved, nil
}

type fakeResolvedChecker struct {
	mu            sync.Mutex
	validateErr   error
	prepareErr    error
	prepared      *fakePreparedChecker
	validateCalls int
	prepareCalls  int
}

func (c *fakeResolvedChecker) Validate() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.validateCalls++
	return c.validateErr
}

func (c *fakeResolvedChecker) Prepare(context.Context) (preparedChecker, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.prepareCalls++
	if c.prepareErr != nil {
		return nil, c.prepareErr
	}
	return c.prepared, nil
}

type checkerCallResult struct {
	result checkerResult
	err    error
}

type checkerCall struct {
	input          string
	actualOutput   string
	expectedOutput string
}

type fakePreparedChecker struct {
	mu      sync.Mutex
	result  checkerResult
	err     error
	results map[string]checkerCallResult
	calls   []checkerCall
}

func (c *fakePreparedChecker) Check(
	_ context.Context,
	input string,
	actualOutput string,
	expectedOutput string,
) (checkerResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls = append(c.calls, checkerCall{
		input:          input,
		actualOutput:   actualOutput,
		expectedOutput: expectedOutput,
	})
	if result, ok := c.results[actualOutput]; ok {
		return result.result, result.err
	}
	return c.result, c.err
}

func testFileSystem(files map[string][]byte) fstest.MapFS {
	fsys := make(fstest.MapFS, len(files))
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: data}
	}
	return fsys
}

func testCompiledArtifact() *model.CompiledArtifact {
	return &model.CompiledArtifact{Data: []byte("binary"), Mode: 0o755}
}

func successfulFakeCompiler() *fakeCompiler {
	return &fakeCompiler{
		result:   model.CompileResult{Succeeded: true},
		artifact: testCompiledArtifact(),
	}
}

func userOKRunResult(stdout string) RunResult {
	return RunResult{ExitCode: 0, Stdout: stdout, Verdict: execution.VerdictOK}
}

func newTestJudgeEngine(runner Runner, compiler *fakeCompiler, checkerModule checker) *JudgeEngine {
	return newTestJudgeEngineWithExternalResources(runner, compiler, checkerModule, nil)
}

func newTestJudgeEngineWithExternalResources(
	runner Runner,
	compiler *fakeCompiler,
	checkerModule checker,
	externalFS fs.FS,
) *JudgeEngine {
	if runner == nil {
		runner = &fakeRunner{}
	}
	if compiler == nil {
		compiler = successfulFakeCompiler()
	}
	if checkerModule == nil {
		checkerModule = newFakeChecker()
	}
	return newJudgeEngine(
		compiler,
		runner,
		checkerModule,
		externalFS,
		10,
		model.DefaultJudgeLimits(),
	)
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
	compiler := &fakeCompiler{result: model.CompileResult{Succeeded: false, Log: "compile failed"}}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(nil, compiler, checkerModule)

	result := engine.Judge(context.Background(), baseJudgeRequest())

	assert.Equal(t, model.JudgeStatusCompileError, result.Status)
	assert.False(t, result.Compile.Succeeded)
	assert.Equal(t, "compile failed", result.Compile.Log)
	assert.Empty(t, result.Cases)
	assert.Equal(t, 1, compiler.calls)
	assert.Zero(t, checkerModule.resolved.prepareCalls)
	assert.Equal(t, 1, result.TotalCount)
}

func TestJudgeEngine_WrongAnswerAfterOK(t *testing.T) {
	runner := &fakeRunner{runResult: userOKRunResult("41\n")}
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepared.result = checkerResult{
		Verdict: model.VerdictWA,
		Message: "1st lines differ - expected: '42', found: '41'",
	}
	engine := newTestJudgeEngine(runner, nil, checkerModule)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{ExpectedOutput: "42\n"},
	))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictWA, result.Cases[0].Verdict)
	assert.Equal(t, "1st lines differ - expected: '42', found: '41'", result.Cases[0].ExtraInfo)
	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Zero(t, result.PassedCount)
	assert.Equal(t, 1, runner.calls)
	assert.Len(t, checkerModule.resolved.prepared.calls, 1)
}

func TestJudgeEngine_CheckerFailureMarksOnlyCurrentCase(t *testing.T) {
	runner := &inputKeyedRunner{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("2\n")},
		"2\n": {result: userOKRunResult("4\n")},
		"3\n": {result: userOKRunResult("6\n")},
	}}
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepared.results = map[string]checkerCallResult{
		"2\n": {result: checkerResult{Verdict: model.VerdictOK}},
		"4\n": {result: checkerResult{Verdict: model.VerdictUKE, Message: "checker timed out"}},
		"6\n": {result: checkerResult{Verdict: model.VerdictOK}},
	}
	engine := newTestJudgeEngine(runner, nil, checkerModule)

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
	runner := &inputKeyedRunner{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("2\n")},
		"2\n": {result: userOKRunResult("4\n")},
		"3\n": {result: RunResult{Verdict: execution.VerdictTLE, ExitCode: 124}},
	}}
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepared.results = map[string]checkerCallResult{
		"2\n": {result: checkerResult{Verdict: model.VerdictOK}},
		"4\n": {result: checkerResult{Verdict: model.VerdictWA, Message: "2nd lines differ"}},
	}
	engine := newTestJudgeEngine(runner, nil, checkerModule)

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
	assert.Len(t, checkerModule.resolved.prepared.calls, 2)
}

func TestJudgeEngine_AllTestCasesPass(t *testing.T) {
	runner := &inputKeyedRunner{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("2\n")},
		"2\n": {result: userOKRunResult("4\n")},
		"3\n": {result: userOKRunResult("6\n")},
	}}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(runner, nil, checkerModule)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "2\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "4\n"},
		model.JudgeTestCase{InputText: "3\n", ExpectedOutput: "6\n"},
	))

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, 3, result.PassedCount)
	assert.Equal(t, 3, result.TotalCount)
	assert.Len(t, checkerModule.resolved.prepared.calls, 3)
}

func TestJudgeEngine_CheckerPrepareFailureReturnsUnknownError(t *testing.T) {
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepareErr = errors.New("checker compilation failed: fatal error: testlib.h missing")
	engine := newTestJudgeEngine(nil, nil, checkerModule)

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

func TestJudgeEngine_ValidateRequest(t *testing.T) {
	tests := []struct {
		name       string
		req        model.JudgeRequest
		checkerErr error
		externalFS fs.FS
		wantErr    string
	}{
		{
			name:       "checker validation error",
			req:        baseJudgeRequest(),
			checkerErr: errors.New("checker dependency missing"),
			wantErr:    "checker dependency missing",
		},
		{
			name: "external input requires resources",
			req: baseJudgeRequest(model.JudgeTestCase{
				InputFile: "cases/1.in",
			}),
			wantErr: `inputFile "cases/1.in" requires external resources`,
		},
		{
			name: "missing external input file",
			req: baseJudgeRequest(model.JudgeTestCase{
				InputFile: "cases/1.in",
			}),
			externalFS: testFileSystem(nil),
			wantErr:    `testcases[0]: inputFile "cases/1.in" is not available`,
		},
		{
			name: "request over configured time limit",
			req: func() model.JudgeRequest {
				req := baseJudgeRequest()
				req.TimeLimit = model.DefaultJudgeLimits().MaxTimeLimitMs + 1
				return req
			}(),
			wantErr: "timeLimit must be at most",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkerModule := newFakeChecker()
			checkerModule.resolved.validateErr = tt.checkerErr
			engine := newTestJudgeEngineWithExternalResources(nil, nil, checkerModule, tt.externalFS)

			err := engine.ValidateRequest(context.Background(), tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestJudgeEngine_Judge_UsesRequestedChecker(t *testing.T) {
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(
		&fakeRunner{runResult: userOKRunResult("YES\n")},
		nil,
		checkerModule,
	)
	req := baseJudgeRequest(model.JudgeTestCase{ExpectedOutput: "YES\n"})
	req.Checker = "yesno"

	result := engine.Judge(context.Background(), req)

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, []string{"yesno"}, checkerModule.references)
}

func TestJudgeEngine_UserRuntimeErrorSkipsChecker(t *testing.T) {
	runner := &fakeRunner{runResult: RunResult{Verdict: execution.VerdictTLE, ExitCode: 124}}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(runner, nil, checkerModule)

	result := engine.Judge(context.Background(), baseJudgeRequest(model.JudgeTestCase{}))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictTLE, result.Cases[0].Verdict)
	assert.Equal(t, 1, runner.calls)
	assert.Empty(t, checkerModule.resolved.prepared.calls)
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

func TestJudgeEngine_CheckerErrorMarksCaseUnknownError(t *testing.T) {
	runner := &inputKeyedRunner{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("42\n")},
		"2\n": {result: userOKRunResult("42\n")},
	}}
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepared.err = errors.New("sandbox boom")
	engine := newTestJudgeEngine(runner, nil, checkerModule)

	result := engine.Judge(context.Background(), baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "42\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "42\n"},
	))

	require.Len(t, result.Cases, 2)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "checker infrastructure error")
	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.Equal(t, 2, runner.calls)
	assert.Len(t, checkerModule.resolved.prepared.calls, 2)
}

type runCallResult struct {
	result RunResult
	err    error
}

type inputKeyedRunner struct {
	mu      sync.Mutex
	results map[string]runCallResult
	calls   int
}

func (r *inputKeyedRunner) PreflightCheck(_ context.Context) error { return nil }

func (r *inputKeyedRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	data, err := io.ReadAll(req.Stdin)
	if err != nil {
		return RunResult{}, fmt.Errorf("read user input: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++

	input := string(data)
	result, ok := r.results[input]
	if !ok {
		return RunResult{}, fmt.Errorf("inputKeyedRunner: no result for input %q", input)
	}
	return result.result, result.err
}

func TestJudgeEngine_DoesNotMutateCallerRequest(t *testing.T) {
	fakeResources := testFileSystem(map[string][]byte{
		"test.in":  []byte("input data"),
		"test.out": []byte("expected output"),
	})
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngineWithExternalResources(
		&fakeRunner{runResult: userOKRunResult("expected output")},
		nil,
		checkerModule,
		fakeResources,
	)
	originalReq := model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{{
			InputFile:          "test.in",
			ExpectedOutputFile: "test.out",
		}},
	}

	result := engine.Judge(context.Background(), originalReq)

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, "test.in", originalReq.TestCases[0].InputFile)
	assert.Equal(t, "test.out", originalReq.TestCases[0].ExpectedOutputFile)
	assert.Empty(t, originalReq.TestCases[0].InputText)
	assert.Empty(t, originalReq.TestCases[0].ExpectedOutput)
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
