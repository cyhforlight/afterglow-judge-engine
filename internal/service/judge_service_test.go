package service

import (
	"context"
	"errors"
	"testing"

	"afterglow-judge-sandbox/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCompiler struct {
	output UserCodeCompileOutput
	err    error
}

func (c *fakeCompiler) Compile(_ context.Context, _ UserCodeCompileRequest) (UserCodeCompileOutput, error) {
	return c.output, c.err
}

type fakeRunner struct {
	preflightErr error
	executeErr   error
	result       model.ExecuteResult
	results      []model.ExecuteResult
	calls        int
}

func (r *fakeRunner) PreflightCheck(_ context.Context) error {
	return r.preflightErr
}

func (r *fakeRunner) Execute(_ context.Context, _ model.ExecuteRequest) (model.ExecuteResult, error) {
	if r.executeErr != nil {
		return model.ExecuteResult{}, r.executeErr
	}
	if len(r.results) == 0 {
		return r.result, nil
	}
	idx := r.calls
	if idx >= len(r.results) {
		idx = len(r.results) - 1
	}
	r.calls++
	return r.results[idx], nil
}

func testCompiledArtifact() *model.CompiledArtifact {
	return &model.CompiledArtifact{
		Name: "program",
		Data: []byte("binary"),
		Mode: 0o755,
	}
}

func TestJudgeEngine_CompileError(t *testing.T) {
	engine := NewJudgeEngine(&fakeRunner{}, &fakeCompiler{output: UserCodeCompileOutput{
		Result: model.CompileResult{Succeeded: false, Log: "compile failed"},
	}})

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "int main(){",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "case1", InputText: "", ExpectedOutput: ""},
		},
	})

	assert.Equal(t, model.VerdictCE, result.Verdict)
	assert.False(t, result.Compile.Succeeded)
	assert.Equal(t, "compile failed", result.Compile.Log)
	assert.Empty(t, result.Cases)
	assert.Equal(t, 1, result.TotalCount)
}

func TestJudgeEngine_WrongAnswerAfterOK(t *testing.T) {
	engine := NewJudgeEngine(&fakeRunner{result: model.ExecuteResult{Verdict: model.VerdictOK, Stdout: "41\n"}}, &fakeCompiler{output: UserCodeCompileOutput{
		Result:          model.CompileResult{Succeeded: true},
		Artifact:        testCompiledArtifact(),
		RuntimeLanguage: model.LanguageCPP,
	}})

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "case1", InputText: "", ExpectedOutput: "42\n"},
		},
	})

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictWA, result.Cases[0].Verdict)
	assert.Equal(t, model.VerdictWA, result.Verdict)
	assert.Equal(t, 0, result.PassedCount)
}

func TestJudgeEngine_TrimTrailingSpaceCompare(t *testing.T) {
	engine := NewJudgeEngine(&fakeRunner{result: model.ExecuteResult{Verdict: model.VerdictOK, Stdout: "42\n\n"}}, &fakeCompiler{output: UserCodeCompileOutput{
		Result:          model.CompileResult{Succeeded: true},
		Artifact:        testCompiledArtifact(),
		RuntimeLanguage: model.LanguageCPP,
	}})

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "case1", InputText: "", ExpectedOutput: "42"},
		},
	})

	require.Len(t, result.Cases, 1)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)
	assert.Equal(t, model.VerdictOK, result.Verdict)
	assert.Equal(t, 1, result.PassedCount)
}

func TestJudgeEngine_AggregateRuntimePriority(t *testing.T) {
	runner := &fakeRunner{results: []model.ExecuteResult{
		{Verdict: model.VerdictRE},
		{Verdict: model.VerdictTLE},
		{Verdict: model.VerdictMLE},
		{Verdict: model.VerdictOLE},
	}}
	engine := NewJudgeEngine(runner, &fakeCompiler{output: UserCodeCompileOutput{
		Result:          model.CompileResult{Succeeded: true},
		Artifact:        testCompiledArtifact(),
		RuntimeLanguage: model.LanguageCPP,
	}})

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "1"},
			{Name: "2"},
			{Name: "3"},
			{Name: "4"},
		},
	})

	assert.Equal(t, model.VerdictOLE, result.Verdict)
}

func TestJudgeEngine_CompilerInfraError(t *testing.T) {
	engine := NewJudgeEngine(&fakeRunner{}, &fakeCompiler{err: errors.New("boom")})

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "case1"},
		},
	})

	assert.Equal(t, model.VerdictUKE, result.Verdict)
	assert.False(t, result.Compile.Succeeded)
	assert.Contains(t, result.Compile.Log, "compile infrastructure error")
}

// TestJudgeEngine_MultipleTestCases_MixedResults verifies that the judge
// correctly handles multiple test cases with different outcomes.
func TestJudgeEngine_MultipleTestCases_MixedResults(t *testing.T) {
	runner := &fakeRunner{results: []model.ExecuteResult{
		{Verdict: model.VerdictOK, Stdout: "2\n", ExitCode: 0},
		{Verdict: model.VerdictOK, Stdout: "4\n", ExitCode: 0},
		{Verdict: model.VerdictOK, Stdout: "5\n", ExitCode: 0}, // Wrong answer
		{Verdict: model.VerdictTLE, Stdout: "", ExitCode: 124},
	}}
	engine := NewJudgeEngine(runner, &fakeCompiler{output: UserCodeCompileOutput{
		Result:          model.CompileResult{Succeeded: true},
		Artifact:        testCompiledArtifact(),
		RuntimeLanguage: model.LanguageCPP,
	}})

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "case-1", InputText: "1\n", ExpectedOutput: "2\n"},
			{Name: "case-2", InputText: "2\n", ExpectedOutput: "4\n"},
			{Name: "case-3", InputText: "3\n", ExpectedOutput: "6\n"}, // Will get WA
			{Name: "case-4", InputText: "4\n", ExpectedOutput: "8\n"}, // Will get TLE
		},
	})

	require.Len(t, result.Cases, 4, "should have 4 case results")

	// Verify individual case results
	assert.Equal(t, "case-1", result.Cases[0].Name)
	assert.Equal(t, model.VerdictOK, result.Cases[0].Verdict)

	assert.Equal(t, "case-2", result.Cases[1].Name)
	assert.Equal(t, model.VerdictOK, result.Cases[1].Verdict)

	assert.Equal(t, "case-3", result.Cases[2].Name)
	assert.Equal(t, model.VerdictWA, result.Cases[2].Verdict, "should be WA due to output mismatch")
	assert.Contains(t, result.Cases[2].ExtraInfo, "does not match")

	assert.Equal(t, "case-4", result.Cases[3].Name)
	assert.Equal(t, model.VerdictTLE, result.Cases[3].Verdict)

	// Verify aggregation: TLE should take priority over WA
	assert.Equal(t, model.VerdictTLE, result.Verdict, "aggregate verdict should be TLE (runtime error priority)")
	assert.Equal(t, 2, result.PassedCount, "should have 2 passed cases")
	assert.Equal(t, 4, result.TotalCount)
}

// TestJudgeEngine_AllTestCasesPass verifies that when all test cases pass,
// the overall verdict is OK.
func TestJudgeEngine_AllTestCasesPass(t *testing.T) {
	runner := &fakeRunner{results: []model.ExecuteResult{
		{Verdict: model.VerdictOK, Stdout: "2\n", ExitCode: 0},
		{Verdict: model.VerdictOK, Stdout: "4\n", ExitCode: 0},
		{Verdict: model.VerdictOK, Stdout: "6\n", ExitCode: 0},
	}}
	engine := NewJudgeEngine(runner, &fakeCompiler{output: UserCodeCompileOutput{
		Result:          model.CompileResult{Succeeded: true},
		Artifact:        testCompiledArtifact(),
		RuntimeLanguage: model.LanguageCPP,
	}})

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{Name: "case-1", InputText: "1\n", ExpectedOutput: "2\n"},
			{Name: "case-2", InputText: "2\n", ExpectedOutput: "4\n"},
			{Name: "case-3", InputText: "3\n", ExpectedOutput: "6\n"},
		},
	})

	assert.Equal(t, model.VerdictOK, result.Verdict, "all cases pass should result in OK")
	assert.Equal(t, 3, result.PassedCount)
	assert.Equal(t, 3, result.TotalCount)
}

// TestJudgeEngine_EmptyTestCases verifies validation catches empty test cases.
func TestJudgeEngine_EmptyTestCases(t *testing.T) {
	engine := NewJudgeEngine(&fakeRunner{}, &fakeCompiler{})

	result := engine.Judge(context.Background(), model.JudgeRequest{
		SourceCode:  "code",
		Language:    model.LanguageCPP,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases:   []model.JudgeTestCase{}, // Empty!
	})

	assert.Equal(t, model.VerdictUKE, result.Verdict)
	assert.False(t, result.Compile.Succeeded)
	assert.Contains(t, result.Compile.Log, "at least one testcase is required")
}

// TestJudgeEngine_SingleTestCase verifies single test case works correctly.
func TestJudgeEngine_SingleTestCase(t *testing.T) {
	runner := &fakeRunner{result: model.ExecuteResult{
		Verdict: model.VerdictOK, Stdout: "42\n", ExitCode: 0,
	}}
	engine := NewJudgeEngine(runner, &fakeCompiler{output: UserCodeCompileOutput{
		Result:          model.CompileResult{Succeeded: true},
		Artifact:        testCompiledArtifact(),
		RuntimeLanguage: model.LanguagePython,
	}})

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

// TestJudgeEngine_InvalidRequest verifies validation of request parameters.
//
//nolint:funlen // Table-driven test with multiple validation scenarios.
func TestJudgeEngine_InvalidRequest(t *testing.T) {
	engine := NewJudgeEngine(&fakeRunner{}, &fakeCompiler{})

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
