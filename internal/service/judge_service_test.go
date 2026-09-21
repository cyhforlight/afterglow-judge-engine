package service

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLanguage struct {
	compiler *fakeLanguageCompiler
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

func (l *fakeLanguage) Resolve(model.Language) (languageCompiler, error) {
	return l.compiler, nil
}

type fakeLanguageCompiler struct {
	program      compiledProgram
	result       model.CompileResult
	err          error
	compileCalls atomic.Int32
}

func (c *fakeLanguageCompiler) Compile(
	_ context.Context,
	_ string,
) (compiledProgram, model.CompileResult, error) {
	c.compileCalls.Add(1)
	return c.program, c.result, c.err
}

type fakeCompiledProgram struct {
	mu        sync.Mutex
	runResult execution.RunResult
	runErr    error
	results   map[string]execution.RunResult
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
		return result, nil
	}
	return p.runResult, p.runErr
}

type fakeChecker struct {
	materializeErr   error
	plan             *fakeCheckerPlan
	materializeCalls atomic.Int32
}

func newFakeChecker() *fakeChecker {
	return &fakeChecker{plan: &fakeCheckerPlan{
		compile:  model.CompileResult{Succeeded: true},
		prepared: &fakePreparedChecker{result: checkerResult{Outcome: checkerAccepted}},
	}}
}

func (*fakeChecker) Close() {}

func (c *fakeChecker) Materialize(checkerChoice) (checkerPlan, error) {
	c.materializeCalls.Add(1)
	if c.materializeErr != nil {
		return nil, c.materializeErr
	}
	return c.plan, nil
}

type fakeCheckerPlan struct {
	prepareErr error
	compile    model.CompileResult
	prepared   *fakePreparedChecker
}

func (p *fakeCheckerPlan) Prepare(context.Context) (checkerPreparation, error) {
	if p.prepareErr != nil {
		return checkerPreparation{}, p.prepareErr
	}
	return checkerPreparation{checker: p.prepared, compile: p.compile}, nil
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
	results map[string]checkerResult
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
		return result, nil
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

func TestJudgeEngine_CompileError(t *testing.T) {
	languageModule := newFakeLanguage()
	languageModule.compiler.program = nil
	languageModule.compiler.result = model.CompileResult{Succeeded: false, Log: "compile failed"}
	engine := newTestJudgeEngine(languageModule, nil)

	result := judgeSuccessfully(t, engine, baseJudgeRequest())

	assert.Equal(t, model.JudgeStatusCompileError, result.Status)
	assert.False(t, result.Compile.Succeeded)
	assert.Equal(t, "compile failed", result.Compile.Log)
	assert.Nil(t, result.CheckerCompile)
	assert.Empty(t, result.Cases)
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
	program := &fakeCompiledProgram{results: map[string]execution.RunResult{
		"1\n": userOKRunResult("2\n"),
		"2\n": userOKRunResult("4\n"),
		"3\n": {Verdict: execution.VerdictTLE, ExitCode: 124},
		"4\n": userOKRunResult("8\n"),
	}}
	checkerModule := newFakeChecker()
	checkerModule.plan.prepared.results = map[string]checkerResult{
		"2\n": {Outcome: checkerAccepted},
		"4\n": {Outcome: checkerFailed, Message: "checker timed out"},
		"8\n": {Outcome: checkerRejected, Message: "4th lines differ"},
	}
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "2\n"},
		model.JudgeTestCase{InputText: "2\n", ExpectedOutput: "4\n"},
		model.JudgeTestCase{InputText: "3\n", ExpectedOutput: "6\n"},
		model.JudgeTestCase{InputText: "4\n", ExpectedOutput: "16\n"},
	))

	require.Len(t, result.Cases, 4)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)
	assert.Equal(t, model.VerdictUKE, result.Cases[1].Verdict)
	assert.Equal(t, "checker timed out", result.Cases[1].ExtraInfo)
	assert.Equal(t, model.VerdictTLE, result.Cases[2].Verdict)
	assert.Equal(t, model.VerdictWA, result.Cases[3].Verdict)
	assert.Equal(t, "4th lines differ", result.Cases[3].ExtraInfo)
	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	require.NotNil(t, result.CheckerCompile)
	assert.True(t, result.CheckerCompile.Succeeded)
}

func TestJudgeEngine_CheckerPrepareInfrastructureFailureReturnsNoCaseResults(t *testing.T) {
	checkerModule := newFakeChecker()
	checkerModule.plan.prepareErr = errors.New("compiler unavailable")
	engine := newTestJudgeEngine(nil, checkerModule)
	req := baseJudgeRequest(
		model.JudgeTestCase{},
	)
	req.CheckerSourceCode = "checker source"

	result := judgeSuccessfully(t, engine, req)

	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.True(t, result.Compile.Succeeded)
	assert.Nil(t, result.CheckerCompile)
	assert.Empty(t, result.Cases)
}

func TestJudgeEngine_CheckerCompileFailureClassifiedBySource(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*model.JudgeRequest)
		wantStatus model.JudgeStatus
	}{
		{
			name:       "builtin checker is a system error",
			configure:  func(*model.JudgeRequest) {},
			wantStatus: model.JudgeStatusSystemError,
		},
		{
			name: "external checker is a system error",
			configure: func(req *model.JudgeRequest) {
				req.Checker = "external:custom.cpp"
			},
			wantStatus: model.JudgeStatusSystemError,
		},
		{
			name: "inline checker is a request error",
			configure: func(req *model.JudgeRequest) {
				req.CheckerSourceCode = "invalid checker source"
			},
			wantStatus: model.JudgeStatusCheckerCompileError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkerModule := newFakeChecker()
			checkerModule.plan.compile = model.CompileResult{Succeeded: false, Log: "syntax error"}
			checkerModule.plan.prepared = nil
			engine := newTestJudgeEngine(nil, checkerModule)
			req := baseJudgeRequest()
			tt.configure(&req)

			result := judgeSuccessfully(t, engine, req)

			assert.Equal(t, tt.wantStatus, result.Status)
			assert.True(t, result.Compile.Succeeded)
			require.NotNil(t, result.CheckerCompile)
			assert.False(t, result.CheckerCompile.Succeeded)
			assert.Equal(t, "syntax error", result.CheckerCompile.Log)
			assert.Empty(t, result.Cases)
		})
	}
}

func TestJudgeEngine_CheckerExecutionFailureClassifiedBySource(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*model.JudgeRequest)
		wantStatus  model.JudgeStatus
		wantVerdict model.Verdict
	}{
		{
			name:        "builtin checker is a system error",
			configure:   func(*model.JudgeRequest) {},
			wantStatus:  model.JudgeStatusSystemError,
			wantVerdict: model.VerdictUKE,
		},
		{
			name: "external checker is a system error",
			configure: func(req *model.JudgeRequest) {
				req.Checker = "external:custom.cpp"
			},
			wantStatus:  model.JudgeStatusSystemError,
			wantVerdict: model.VerdictUKE,
		},
		{
			name: "inline checker is a request error",
			configure: func(req *model.JudgeRequest) {
				req.CheckerSourceCode = "checker source"
			},
			wantStatus:  model.JudgeStatusCheckerExecutionError,
			wantVerdict: model.VerdictCheckerExecutionError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkerModule := newFakeChecker()
			checkerModule.plan.prepared.result = checkerResult{
				Outcome: checkerFailed,
				Message: "checker timed out",
			}
			engine := newTestJudgeEngine(nil, checkerModule)
			req := baseJudgeRequest()
			tt.configure(&req)

			result := judgeSuccessfully(t, engine, req)

			assert.Equal(t, tt.wantStatus, result.Status)
			require.Len(t, result.Cases, 1)
			assert.Equal(t, tt.wantVerdict, result.Cases[0].Verdict)
			assert.Equal(t, "checker timed out", result.Cases[0].ExtraInfo)
		})
	}
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
	assert.Zero(t, languageModule.compiler.compileCalls.Load())
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
			checkerModule := newFakeChecker()
			checkerModule.materializeErr = tt.materializeErr
			engine := newTestJudgeEngineWithExternalResources(newFakeLanguage(), checkerModule, tt.externalFS)

			result, err := engine.Judge(t.Context(), tt.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Zero(t, result)
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
		{name: "named and inline checker", mutate: func(req *model.JudgeRequest) {
			req.Checker = "default"
			req.CheckerSourceCode = "checker source"
		}, wantErr: "checker and checkerSourceCode cannot be provided together"},
		{name: "blank inline checker", mutate: func(req *model.JudgeRequest) {
			req.CheckerSourceCode = " \n\t"
		}, wantErr: "checkerSourceCode must not be blank"},
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

func TestJudgeEngine_UserRuntimeErrorSkipsChecker(t *testing.T) {
	program := &fakeCompiledProgram{runResult: execution.RunResult{Verdict: execution.VerdictTLE, ExitCode: 124}}
	checkerModule := newFakeChecker()
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)

	result := judgeSuccessfully(t, engine, baseJudgeRequest(model.JudgeTestCase{}))

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictTLE, result.Cases[0].Verdict)
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

func TestJudgeEngine_InlineCheckerInfrastructureErrorRemainsSystemError(t *testing.T) {
	program := &fakeCompiledProgram{runResult: userOKRunResult("42\n")}
	checkerModule := newFakeChecker()
	checkerModule.plan.prepared.err = errors.New("sandbox boom")
	engine := newTestJudgeEngine(newFakeLanguageWithProgram(program), checkerModule)
	req := baseJudgeRequest(
		model.JudgeTestCase{InputText: "1\n", ExpectedOutput: "42\n"},
	)
	req.CheckerSourceCode = "checker source"

	result := judgeSuccessfully(t, engine, req)

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictUKE, result.Cases[0].Verdict)
	assert.Contains(t, result.Cases[0].ExtraInfo, "checker infrastructure error")
	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
}

func TestAggregateStatus_SystemErrorTakesPriorityOverCheckerExecutionError(t *testing.T) {
	status := aggregateStatus([]model.JudgeCaseResult{
		{Verdict: model.VerdictCheckerExecutionError},
		{Verdict: model.VerdictUKE},
	})

	assert.Equal(t, model.JudgeStatusSystemError, status)
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
