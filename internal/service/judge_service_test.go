package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLanguage struct {
	mu         sync.Mutex
	resolveErr error
	toolchain  *fakeLanguageToolchain
	languages  []model.Language
}

func newFakeLanguage() *fakeLanguage {
	return newFakeLanguageWithProgram(&fakeCompiledProgram{
		runResult: userOKRunResult(""),
	})
}

func newFakeLanguageWithProgram(program compiledProgram) *fakeLanguage {
	return &fakeLanguage{toolchain: &fakeLanguageToolchain{
		program: program,
		result:  model.CompileResult{Succeeded: true},
	}}
}

func (l *fakeLanguage) Resolve(lang model.Language) (languageToolchain, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.languages = append(l.languages, lang)
	if l.resolveErr != nil {
		return nil, l.resolveErr
	}
	return l.toolchain, nil
}

type fakeLanguageToolchain struct {
	mu      sync.Mutex
	program compiledProgram
	result  model.CompileResult
	err     error
	sources []string
}

func (t *fakeLanguageToolchain) Compile(
	_ context.Context,
	source string,
) (compiledProgram, model.CompileResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.sources = append(t.sources, source)
	return t.program, t.result, t.err
}

type runCallResult struct {
	result RunResult
	err    error
}

type fakeCompiledProgram struct {
	mu        sync.Mutex
	runResult RunResult
	runErr    error
	results   map[string]runCallResult
	inputs    []string
}

func (p *fakeCompiledProgram) Run(
	_ context.Context,
	input string,
	_, _ int,
) (RunResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.inputs = append(p.inputs, input)
	if result, ok := p.results[input]; ok {
		return result.result, result.err
	}
	if p.results != nil {
		return RunResult{}, fmt.Errorf("fake program: no result for input %q", input)
	}
	return p.runResult, p.runErr
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

func userOKRunResult(stdout string) RunResult {
	return RunResult{ExitCode: 0, Stdout: stdout, Verdict: execution.VerdictOK}
}

func newTestJudgeEngine(languageModule language, checkerModule checker) *JudgeEngine {
	return newTestJudgeEngineWithExternalResources(languageModule, checkerModule, nil)
}

func newTestJudgeEngineWithExternalResources(
	languageModule language,
	checkerModule checker,
	externalFS fs.FS,
) *JudgeEngine {
	if languageModule == nil {
		languageModule = newFakeLanguage()
	}
	if checkerModule == nil {
		checkerModule = newFakeChecker()
	}
	return newJudgeEngine(
		languageModule,
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

func judgeSuccessfully(t *testing.T, engine *JudgeEngine, req model.JudgeRequest) model.JudgeResult {
	t.Helper()

	result, err := engine.Judge(t.Context(), req)
	require.NoError(t, err)
	return result
}

func TestJudgeEngine_CompileError(t *testing.T) {
	languageModule := newFakeLanguage()
	languageModule.toolchain.program = nil
	languageModule.toolchain.result = model.CompileResult{Succeeded: false, Log: "compile failed"}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(languageModule, checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest())

	assert.Equal(t, model.JudgeStatusCompileError, result.Status)
	assert.False(t, result.Compile.Succeeded)
	assert.Equal(t, "compile failed", result.Compile.Log)
	assert.Empty(t, result.Cases)
	assert.Len(t, languageModule.toolchain.sources, 1)
	assert.Zero(t, checkerModule.resolved.prepareCalls)
}

func TestJudgeEngine_WrongAnswerAfterOK(t *testing.T) {
	program := &fakeCompiledProgram{runResult: userOKRunResult("41\n")}
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepared.result = checkerResult{
		Verdict: model.VerdictWA,
		Message: "1st lines differ - expected: '42', found: '41'",
	}
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(
		model.JudgeTestCase{ExpectedOutput: "42\n"},
	))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictWA, result.Cases[0].Verdict)
	assert.Equal(t, "1st lines differ - expected: '42', found: '41'", result.Cases[0].ExtraInfo)
	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Len(t, program.inputs, 1)
	assert.Len(t, checkerModule.resolved.prepared.calls, 1)
}

func TestJudgeEngine_CheckerFailureMarksOnlyCurrentCase(t *testing.T) {
	program := &fakeCompiledProgram{results: map[string]runCallResult{
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
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(
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
}

func TestJudgeEngine_CompilerInfraError(t *testing.T) {
	languageModule := newFakeLanguage()
	languageModule.toolchain.err = errors.New("boom")
	engine := newTestJudgeEngine(languageModule, nil)

	result := judgeSuccessfully(t, engine, baseJudgeRequest())

	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.False(t, result.Compile.Succeeded)
	assert.Contains(t, result.Compile.Log, "compile infrastructure error")
}

func TestJudgeEngine_MultipleTestCases_MixedResults(t *testing.T) {
	program := &fakeCompiledProgram{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("2\n")},
		"2\n": {result: userOKRunResult("4\n")},
		"3\n": {result: RunResult{Verdict: execution.VerdictTLE, ExitCode: 124}},
	}}
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepared.results = map[string]checkerCallResult{
		"2\n": {result: checkerResult{Verdict: model.VerdictOK}},
		"4\n": {result: checkerResult{Verdict: model.VerdictWA, Message: "2nd lines differ"}},
	}
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(
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
	assert.Len(t, checkerModule.resolved.prepared.calls, 2)
}

func TestJudgeEngine_AllTestCasesPass(t *testing.T) {
	program := &fakeCompiledProgram{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("2\n")},
		"2\n": {result: userOKRunResult("4\n")},
		"3\n": {result: userOKRunResult("6\n")},
	}}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "2\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "4\n"},
		model.JudgeTestCase{InputText: "3\n", ExpectedOutput: "6\n"},
	))

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Len(t, result.Cases, 3)
	assert.Len(t, checkerModule.resolved.prepared.calls, 3)
}

func TestJudgeEngine_CheckerPrepareFailureReturnsNoCaseResults(t *testing.T) {
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepareErr = errors.New("checker compilation failed: fatal error: testlib.h missing")
	engine := newTestJudgeEngine(nil, checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(
		model.JudgeTestCase{},
		model.JudgeTestCase{},
	))

	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.True(t, result.Compile.Succeeded)
	assert.Empty(t, result.Cases)
}

func TestJudgeEngine_RejectsInvalidRequest(t *testing.T) {
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
				InputFile:          "cases/1.in",
				ExpectedOutputFile: "cases/1.out",
			}),
			wantErr: `inputFile "cases/1.in" requires external resources`,
		},
		{
			name: "missing external input file",
			req: baseJudgeRequest(model.JudgeTestCase{
				InputFile:          "cases/1.in",
				ExpectedOutputFile: "cases/1.out",
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
			languageModule := newFakeLanguage()
			checkerModule := newFakeChecker()
			checkerModule.resolved.validateErr = tt.checkerErr
			engine := newTestJudgeEngineWithExternalResources(languageModule, checkerModule, tt.externalFS)

			result, err := engine.Judge(t.Context(), tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Zero(t, result)
			assert.Empty(t, languageModule.toolchain.sources)
			assert.Zero(t, checkerModule.resolved.prepareCalls)
		})
	}
}

func TestJudgeEngine_Judge_UsesRequestedChecker(t *testing.T) {
	checkerModule := newFakeChecker()
	program := &fakeCompiledProgram{runResult: userOKRunResult("YES\n")}
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)
	req := baseJudgeRequest(model.JudgeTestCase{ExpectedOutput: "YES\n"})
	req.Checker = "yesno"

	result := judgeSuccessfully(t, engine, req)

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, []string{"yesno"}, checkerModule.references)
}

func TestJudgeEngine_UserRuntimeErrorSkipsChecker(t *testing.T) {
	program := &fakeCompiledProgram{runResult: RunResult{Verdict: execution.VerdictTLE, ExitCode: 124}}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(model.JudgeTestCase{}))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictTLE, result.Cases[0].Verdict)
	assert.Len(t, program.inputs, 1)
	assert.Empty(t, checkerModule.resolved.prepared.calls)
}

func TestJudgeEngine_UserRunInfrastructureErrorMarksCaseUnknown(t *testing.T) {
	program := &fakeCompiledProgram{runErr: errors.New("sandbox unavailable")}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(model.JudgeTestCase{}))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "infrastructure error: sandbox unavailable")
	assert.Empty(t, checkerModule.resolved.prepared.calls)
}

func TestJudgeEngine_UsesRequestedLanguage(t *testing.T) {
	languageModule := newFakeLanguage()
	engine := newTestJudgeEngine(languageModule, nil)
	req := baseJudgeRequest()
	req.Language = model.LanguageJava

	result := judgeSuccessfully(t, engine, req)

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, []model.Language{model.LanguageJava}, languageModule.languages)
	assert.Equal(t, []string{req.SourceCode}, languageModule.toolchain.sources)
}

func TestJudgeEngine_CheckerErrorMarksCaseUnknownError(t *testing.T) {
	program := &fakeCompiledProgram{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("42\n")},
		"2\n": {result: userOKRunResult("42\n")},
	}}
	checkerModule := newFakeChecker()
	checkerModule.resolved.prepared.err = errors.New("sandbox boom")
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "42\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "42\n"},
	))

	require.Len(t, result.Cases, 2)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "checker infrastructure error")
	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.Len(t, program.inputs, 2)
	assert.Len(t, checkerModule.resolved.prepared.calls, 2)
}

func TestJudgeEngine_DoesNotMutateCallerRequest(t *testing.T) {
	fakeResources := testFileSystem(map[string][]byte{
		"test.in":  []byte("input data"),
		"test.out": []byte("expected output"),
	})
	checkerModule := newFakeChecker()
	program := &fakeCompiledProgram{runResult: userOKRunResult("expected output")}
	engine := newTestJudgeEngineWithExternalResources(
		newFakeLanguageWithProgram(program),
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

	result := judgeSuccessfully(t, engine, originalReq)

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
