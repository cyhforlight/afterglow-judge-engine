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
	compiler   *fakeLanguageCompiler
	languages  []model.Language
}

func newFakeLanguage() *fakeLanguage {
	return newFakeLanguageWithProgram(&fakeCompiledProgram{
		runResult: userOKRunResult(""),
	})
}

func newFakeLanguageWithProgram(program compiledProgram) *fakeLanguage {
	return &fakeLanguage{compiler: &fakeLanguageCompiler{
		program: program,
		result:  model.CompileResult{Succeeded: true},
	}}
}

func (l *fakeLanguage) Resolve(lang model.Language) (languageCompiler, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.languages = append(l.languages, lang)
	if l.resolveErr != nil {
		return nil, l.resolveErr
	}
	return l.compiler, nil
}

type fakeLanguageCompiler struct {
	mu      sync.Mutex
	program compiledProgram
	result  model.CompileResult
	err     error
	sources []string
}

func (c *fakeLanguageCompiler) Compile(
	_ context.Context,
	source string,
) (compiledProgram, model.CompileResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sources = append(c.sources, source)
	return c.program, c.result, c.err
}

type runCallResult struct {
	result execution.RunResult
	err    error
}

type fakeCompiledProgram struct {
	mu        sync.Mutex
	runResult execution.RunResult
	runErr    error
	results   map[string]runCallResult
	inputs    []string
}

func (p *fakeCompiledProgram) Run(
	_ context.Context,
	input string,
	_, _ uint32,
) (execution.RunResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.inputs = append(p.inputs, input)
	if result, ok := p.results[input]; ok {
		return result.result, result.err
	}
	if p.results != nil {
		return execution.RunResult{}, fmt.Errorf("fake program: no result for input %q", input)
	}
	return p.runResult, p.runErr
}

type fakeChecker struct {
	mu             sync.Mutex
	materializeErr error
	plan           *fakeCheckerPlan
	references     []string
}

func newFakeChecker() *fakeChecker {
	return &fakeChecker{plan: &fakeCheckerPlan{
		prepared: &fakePreparedChecker{result: checkerResult{Verdict: model.VerdictOK}},
	}}
}

func (c *fakeChecker) Materialize(location checkerLocation) (checkerPlan, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.references = append(c.references, location.path)
	if c.materializeErr != nil {
		return nil, c.materializeErr
	}
	return c.plan, nil
}

func (c *fakeChecker) materializeCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.references)
}

type fakeCheckerPlan struct {
	mu           sync.Mutex
	prepareErr   error
	prepared     *fakePreparedChecker
	prepareCalls int
}

func (p *fakeCheckerPlan) Prepare(context.Context) (preparedChecker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.prepareCalls++
	if p.prepareErr != nil {
		return nil, p.prepareErr
	}
	return p.prepared, nil
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

func userOKRunResult(stdout string) execution.RunResult {
	return execution.RunResult{ExitCode: 0, Stdout: stdout, Verdict: execution.VerdictOK}
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

func TestNewJudgeEngine_RejectsNonPositiveConcurrency(t *testing.T) {
	for _, maxConcurrent := range []int{0, -1} {
		engine, err := NewJudgeEngine(nil, checkerTestFS(), nil, maxConcurrent)
		assert.Nil(t, engine)
		require.ErrorContains(t, err, "max concurrent judges must be positive")
	}
}

func TestJudgeEngine_CompileError(t *testing.T) {
	languageModule := newFakeLanguage()
	languageModule.compiler.program = nil
	languageModule.compiler.result = model.CompileResult{Succeeded: false, Log: "compile failed"}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(languageModule, checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest())

	assert.Equal(t, model.JudgeStatusCompileError, result.Status)
	assert.False(t, result.Compile.Succeeded)
	assert.Equal(t, "compile failed", result.Compile.Log)
	assert.Empty(t, result.Cases)
	assert.Len(t, languageModule.compiler.sources, 1)
	assert.Zero(t, checkerModule.plan.prepareCalls)
}

func TestJudgeEngine_CheckerFailureMarksOnlyCurrentCase(t *testing.T) {
	program := &fakeCompiledProgram{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("2\n")},
		"2\n": {result: userOKRunResult("4\n")},
		"3\n": {result: userOKRunResult("6\n")},
	}}
	checkerModule := newFakeChecker()
	checkerModule.plan.prepared.results = map[string]checkerCallResult{
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
	languageModule.compiler.err = errors.New("boom")
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
		"3\n": {result: execution.RunResult{Verdict: execution.VerdictTLE, ExitCode: 124}},
	}}
	checkerModule := newFakeChecker()
	checkerModule.plan.prepared.results = map[string]checkerCallResult{
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
	assert.Len(t, checkerModule.plan.prepared.calls, 2)
}

func TestJudgeEngine_CheckerPrepareFailureReturnsNoCaseResults(t *testing.T) {
	checkerModule := newFakeChecker()
	checkerModule.plan.prepareErr = errors.New("checker compilation failed: fatal error: testlib.h missing")
	engine := newTestJudgeEngine(nil, checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(
		model.JudgeTestCase{},
		model.JudgeTestCase{},
	))

	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.True(t, result.Compile.Succeeded)
	assert.Empty(t, result.Cases)
}

func TestJudgeEngine_TestDataMaterializeFailureRejectsBeforeCompile(t *testing.T) {
	languageModule := newFakeLanguage()
	externalFS := testFileSystem(map[string][]byte{"test.in": []byte("input")})
	engine := newTestJudgeEngineWithExternalResources(
		languageModule,
		nil,
		externalFS,
	)

	result, err := engine.Judge(t.Context(), baseJudgeRequest(
		model.JudgeTestCase{ExpectedOutput: "output"},
		model.JudgeTestCase{InputFile: "test.in", ExpectedOutputFile: "test.out"},
	))

	require.ErrorContains(t, err, `testcases[1]: expectedOutputFile "test.out" is not available`)
	assert.Zero(t, result)
	assert.Empty(t, languageModule.compiler.sources)
}

func TestJudgeEngine_RejectsUnmaterializableRequest(t *testing.T) {
	tests := []struct {
		name           string
		req            model.JudgeRequest
		materializeErr error
		externalFS     fs.FS
		wantErr        string
	}{
		{
			name:           "checker materialization error",
			req:            baseJudgeRequest(),
			materializeErr: errors.New("checker dependency missing"),
			wantErr:        "checker dependency missing",
		},
		{
			name: "external input requires resources",
			req: baseJudgeRequest(model.JudgeTestCase{
				InputFile:          "cases/1.in",
				ExpectedOutputFile: "cases/1.out",
			}),
			wantErr: `inputFile "cases/1.in" requires external resources`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			languageModule := newFakeLanguage()
			checkerModule := newFakeChecker()
			checkerModule.materializeErr = tt.materializeErr
			engine := newTestJudgeEngineWithExternalResources(languageModule, checkerModule, tt.externalFS)

			result, err := engine.Judge(t.Context(), tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Zero(t, result)
			assert.Empty(t, languageModule.compiler.sources)
			assert.Zero(t, checkerModule.plan.prepareCalls)
		})
	}
}

func TestJudgeEngine_RejectsMalformedRequest(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*model.JudgeRequest)
		wantErr string
	}{
		{name: "missing source", mutate: func(req *model.JudgeRequest) { req.SourceCode = "" }, wantErr: "sourceCode is required"},
		{name: "missing language", mutate: func(req *model.JudgeRequest) { req.Language = model.LanguageUnknown }, wantErr: "language is required"},
		{name: "unsupported language", mutate: func(req *model.JudgeRequest) { req.Language = model.Language("Rust") }, wantErr: "unsupported language"},
		{name: "zero time limit", mutate: func(req *model.JudgeRequest) { req.TimeLimit = 0 }, wantErr: "timeLimit must be positive"},
		{name: "zero memory limit", mutate: func(req *model.JudgeRequest) { req.MemoryLimit = 0 }, wantErr: "memoryLimit must be positive"},
		{name: "missing testcases", mutate: func(req *model.JudgeRequest) { req.TestCases = nil }, wantErr: "testcases must not be empty"},
		{name: "too many testcases", mutate: func(req *model.JudgeRequest) { req.TestCases = make([]model.JudgeTestCase, maxTestCases+1) }, wantErr: "testcases must contain at most"},
		{name: "mixed testcase data", mutate: func(req *model.JudgeRequest) {
			req.TestCases = []model.JudgeTestCase{{InputText: "x", InputFile: "1.in", ExpectedOutputFile: "1.out"}}
		}, wantErr: "cannot mix text and file data"},
		{name: "incomplete file pair", mutate: func(req *model.JudgeRequest) { req.TestCases = []model.JudgeTestCase{{InputFile: "1.in"}} }, wantErr: "must be provided together"},
		{name: "invalid input file path", mutate: func(req *model.JudgeRequest) {
			req.TestCases = []model.JudgeTestCase{{InputFile: "../1.in", ExpectedOutputFile: "1.out"}}
		}, wantErr: "inputFile must be a valid relative path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := baseJudgeRequest()
			tt.mutate(&req)
			engine := newTestJudgeEngine(newLanguage(nil), nil)

			result, err := engine.Judge(t.Context(), req)
			assert.Zero(t, result)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestJudgeEngine_UsesRequestedLanguageAndChecker(t *testing.T) {
	languageModule := newFakeLanguage()
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(languageModule, checkerModule)
	req := baseJudgeRequest(model.JudgeTestCase{ExpectedOutput: "YES\n"})
	req.Language = model.LanguageJava
	req.Checker = "yesno"

	result := judgeSuccessfully(t, engine, req)

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, []model.Language{model.LanguageJava}, languageModule.languages)
	assert.Equal(t, []string{req.SourceCode}, languageModule.compiler.sources)
	assert.Equal(t, []string{"yesno"}, checkerModule.references)
}

func TestJudgeEngine_UserRuntimeErrorSkipsChecker(t *testing.T) {
	program := &fakeCompiledProgram{runResult: execution.RunResult{Verdict: execution.VerdictTLE, ExitCode: 124}}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(model.JudgeTestCase{}))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictTLE, result.Cases[0].Verdict)
	assert.Len(t, program.inputs, 1)
	assert.Empty(t, checkerModule.plan.prepared.calls)
}

func TestJudgeEngine_UserRunInfrastructureErrorMarksCaseUnknown(t *testing.T) {
	program := &fakeCompiledProgram{runErr: errors.New("sandbox unavailable")}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(model.JudgeTestCase{}))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "infrastructure error: sandbox unavailable")
	assert.Empty(t, checkerModule.plan.prepared.calls)
}

func TestJudgeEngine_CheckerErrorMarksCaseUnknownError(t *testing.T) {
	program := &fakeCompiledProgram{results: map[string]runCallResult{
		"1\n": {result: userOKRunResult("42\n")},
		"2\n": {result: userOKRunResult("42\n")},
	}}
	checkerModule := newFakeChecker()
	checkerModule.plan.prepared.err = errors.New("sandbox boom")
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
	assert.Len(t, checkerModule.plan.prepared.calls, 2)
}

func TestJudgeEngine_MaterializesExternalTestCase(t *testing.T) {
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
	req := baseJudgeRequest(model.JudgeTestCase{
		InputFile:          "test.in",
		ExpectedOutputFile: "test.out",
	})

	result := judgeSuccessfully(t, engine, req)

	assert.Equal(t, model.JudgeStatusOK, result.Status)
	assert.Equal(t, []string{"input data"}, program.inputs)
	assert.Equal(t, []checkerCall{{
		input:          "input data",
		actualOutput:   "expected output",
		expectedOutput: "expected output",
	}}, checkerModule.plan.prepared.calls)
}

func TestAggregateStatus(t *testing.T) {
	tests := []struct {
		name     string
		cases    []model.JudgeCaseResult
		expected model.JudgeStatus
	}{
		{"all non-infrastructure verdicts return OK", []model.JudgeCaseResult{
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
