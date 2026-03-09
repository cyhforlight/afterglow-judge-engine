package service

import (
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSandboxFailure_CompileError tests compilation errors.
func TestSandboxFailure_CompileError(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name     string
		language model.Language
		filePath string
	}{
		{"C syntax error", model.LanguageC, "ce/ce_syntax_error.c"},
		{"C++ syntax error", model.LanguageCPP, "ce/ce_syntax_error.cpp"},
		{"Java wrong class name", model.LanguageJava, "ce/ce_wrong_class_name.java"},
		{"Python syntax error", model.LanguagePython, "ce/ce_syntax_error.py"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newIntegrationContext(t, 60*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			compiler := newCompilerForTest(t)
			compileOut := compileProgram(t, serviceIntegrationEnv{
				ctx:      ctx,
				compiler: compiler,
				runner:   nil,
			}, UserCodeCompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})

			assert.False(t, compileOut.Result.Succeeded, "expected compilation to fail")
			assert.NotEmpty(t, compileOut.Result.Log, "expected error log to be non-empty")
		})
	}
}

// TestSandboxFailure_TimeLimit tests time limit exceeded.
func TestSandboxFailure_TimeLimit(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name     string
		language model.Language
		filePath string
	}{
		{"C infinite loop", model.LanguageC, "tle/tle_infinite_loop.c"},
		{"C++ infinite loop", model.LanguageCPP, "tle/tle_infinite_loop.cpp"},
		{"Java infinite loop", model.LanguageJava, "tle/tle_infinite_loop.java"},
		{"Python infinite loop", model.LanguagePython, "tle/tle_infinite_loop.py"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newIntegrationContext(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			compiler := newCompilerForTest(t)
			compileOut := compileProgram(t, serviceIntegrationEnv{
				ctx:      ctx,
				compiler: compiler,
				runner:   nil,
			}, UserCodeCompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})
			require.True(t, compileOut.Result.Succeeded, "compilation should succeed")

			runner := newRunnerForTest(t)
			execResult, err := runner.Execute(ctx, model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       "",
				Language:    tt.language,
				TimeLimit:   1000,
				MemoryLimit: 256,
			})
			require.NoError(t, err)

			assert.Equal(t, model.VerdictTLE, execResult.Verdict, "expected TLE verdict")
		})
	}
}

// TestSandboxFailure_MemoryLimit tests memory limit exceeded.
func TestSandboxFailure_MemoryLimit(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name     string
		language model.Language
		filePath string
	}{
		{"C malloc blocks", model.LanguageC, "mle/mle_malloc_blocks.c"},
		{"C++ vector push", model.LanguageCPP, "mle/mle_vector_push.cpp"},
		{"Java ArrayList", model.LanguageJava, "mle/mle_array_list.java"},
		{"Python list append", model.LanguagePython, "mle/mle_list_append.py"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newIntegrationContext(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			compiler := newCompilerForTest(t)
			compileOut := compileProgram(t, serviceIntegrationEnv{
				ctx:      ctx,
				compiler: compiler,
				runner:   nil,
			}, UserCodeCompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})
			require.True(t, compileOut.Result.Succeeded, "compilation should succeed")

			runner := newRunnerForTest(t)
			execResult, err := runner.Execute(ctx, model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       "",
				Language:    tt.language,
				TimeLimit:   2000,
				MemoryLimit: 64,
			})
			require.NoError(t, err)

			assert.Equal(t, model.VerdictMLE, execResult.Verdict, "expected MLE verdict")
		})
	}
}

// TestSandboxFailure_RuntimeError tests runtime errors.
func TestSandboxFailure_RuntimeError(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name     string
		language model.Language
		filePath string
	}{
		{"C abort", model.LanguageC, "re/re_abort.c"},
		{"C++ segfault", model.LanguageCPP, "re/re_segfault.cpp"},
		{"C++ vector at", model.LanguageCPP, "re/re_vector_at.cpp"},
		{"C++ null dereference", model.LanguageCPP, "re/re_null_dereference.cpp"},
		{"Java null pointer", model.LanguageJava, "re/re_null_pointer.java"},
		{"Python index error", model.LanguagePython, "re/re_index_error.py"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newIntegrationContext(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			compiler := newCompilerForTest(t)
			compileOut := compileProgram(t, serviceIntegrationEnv{
				ctx:      ctx,
				compiler: compiler,
				runner:   nil,
			}, UserCodeCompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})
			require.True(t, compileOut.Result.Succeeded, "compilation should succeed")

			runner := newRunnerForTest(t)
			execResult, err := runner.Execute(ctx, model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       "",
				Language:    tt.language,
				TimeLimit:   2000,
				MemoryLimit: 256,
			})
			require.NoError(t, err)

			assert.Equal(t, model.VerdictRE, execResult.Verdict, "expected RE verdict")
		})
	}
}

// TestSandboxFailure_OutputLimit tests output limit exceeded.
func TestSandboxFailure_OutputLimit(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name     string
		language model.Language
		filePath string
	}{
		{"C++ infinite output", model.LanguageCPP, "ole/ole_infinite_output.cpp"},
		{"Python infinite print", model.LanguagePython, "ole/ole_infinite_print.py"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newIntegrationContext(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			compiler := newCompilerForTest(t)
			compileOut := compileProgram(t, serviceIntegrationEnv{
				ctx:      ctx,
				compiler: compiler,
				runner:   nil,
			}, UserCodeCompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})
			require.True(t, compileOut.Result.Succeeded, "compilation should succeed")

			runner := newRunnerForTest(t)
			execResult, err := runner.Execute(ctx, model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       "",
				Language:    tt.language,
				TimeLimit:   2000,
				MemoryLimit: 256,
			})
			require.NoError(t, err)

			assert.Equal(t, model.VerdictOLE, execResult.Verdict, "expected OLE verdict")
		})
	}
}

// TestSandboxFailure_PolicyViolation tests policy violations.
//
// NOTE: Currently skipped because the sandbox does not have seccomp configuration.
// These tests require system call filtering to properly detect policy violations.
// Without seccomp:
// - Fork bombs trigger TLE (resource exhaustion) instead of RE
// - System calls succeed normally instead of being blocked
//
// TODO: Enable these tests after adding seccomp configuration to sandboxSecurityOpts()
func TestSandboxFailure_PolicyViolation(t *testing.T) {
	t.Skip("Policy violation detection requires seccomp configuration (not yet implemented)")

	requireServiceIntegrationTest(t)

	tests := []struct {
		name     string
		language model.Language
		filePath string
	}{
		{"C fork bomb", model.LanguageC, "policy/policy_fork_bomb.c"},
		{"Python system call", model.LanguagePython, "policy/policy_system_call.py"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newIntegrationContext(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			compiler := newCompilerForTest(t)
			compileOut := compileProgram(t, serviceIntegrationEnv{
				ctx:      ctx,
				compiler: compiler,
				runner:   nil,
			}, UserCodeCompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})
			require.True(t, compileOut.Result.Succeeded, "compilation should succeed")

			runner := newRunnerForTest(t)
			execResult, err := runner.Execute(ctx, model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       "",
				Language:    tt.language,
				TimeLimit:   2000,
				MemoryLimit: 256,
			})
			require.NoError(t, err)

			// POLICY violations are treated as RE or UKE
			assert.Contains(t, []model.Verdict{model.VerdictRE, model.VerdictUKE},
				execResult.Verdict, "expected RE or UKE verdict for policy violation")
		})
	}
}
