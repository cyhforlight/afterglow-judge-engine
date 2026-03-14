package service

import (
	"strings"
	"testing"
	"time"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runUserProgram is a test helper that executes a compiled artifact using the Runner primitive.
func runUserProgram(t *testing.T, env serviceIntegrationEnv, artifact *model.CompiledArtifact, lang model.Language, input string, timeLimit, memoryLimit int) RunResult {
	t.Helper()

	profile, err := ProfileForLanguage(lang)
	require.NoError(t, err)

	programMode := artifact.Mode
	if programMode == 0 {
		programMode = profile.Run.FileMode
	}

	containerPath := runMountDir + "/" + profile.Run.ArtifactName
	runOut, err := env.runner.Run(env.ctx, RunRequest{
		Files: []workspace.File{{
			Name:    profile.Run.ArtifactName,
			Content: artifact.Data,
			Mode:    programMode,
		}},
		ImageRef: profile.Run.ImageRef,
		Command:  profile.Run.RuntimeCommand(containerPath),
		Cwd:      runMountDir,
		Stdin:    strings.NewReader(input),
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   timeLimit,
			WallTimeMs:  timeLimit * sandbox.WallTimeMultiplier,
			MemoryMB:    memoryLimit,
			OutputBytes: sandbox.DefaultExecutionOutputLimitBytes,
		},
	})
	require.NoError(t, err)
	return runOut
}

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
			t.Parallel()
			env := newServiceIntegrationEnv(t, 60*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			_, result := compileProgram(t, env, tt.language, sourceCode)

			assert.False(t, result.Succeeded, "expected compilation to fail")
			assert.NotEmpty(t, result.Log, "expected error log to be non-empty")
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
			t.Parallel()
			env := newServiceIntegrationEnv(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			artifact, result := compileProgram(t, env, tt.language, sourceCode)
			require.True(t, result.Succeeded, "compilation should succeed")

			runOut := runUserProgram(t, env, artifact, tt.language, "", 1000, 256)
			assert.Equal(t, sandbox.VerdictTLE, runOut.Verdict, "expected TLE verdict")
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
			t.Parallel()
			env := newServiceIntegrationEnv(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			artifact, result := compileProgram(t, env, tt.language, sourceCode)
			require.True(t, result.Succeeded, "compilation should succeed")

			runOut := runUserProgram(t, env, artifact, tt.language, "", 2000, 64)
			assert.Equal(t, sandbox.VerdictMLE, runOut.Verdict, "expected MLE verdict")
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
			t.Parallel()
			env := newServiceIntegrationEnv(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			artifact, result := compileProgram(t, env, tt.language, sourceCode)
			require.True(t, result.Succeeded, "compilation should succeed")

			runOut := runUserProgram(t, env, artifact, tt.language, "", 2000, 256)
			assert.Equal(t, sandbox.VerdictRE, runOut.Verdict, "expected RE verdict")
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
			t.Parallel()
			env := newServiceIntegrationEnv(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			artifact, result := compileProgram(t, env, tt.language, sourceCode)
			require.True(t, result.Succeeded, "compilation should succeed")

			runOut := runUserProgram(t, env, artifact, tt.language, "", 2000, 256)
			assert.Equal(t, sandbox.VerdictOLE, runOut.Verdict, "expected OLE verdict")
		})
	}
}

// TestSandboxFailure_PolicyViolation tests policy violations.
//
// NOTE: Currently skipped because the sandbox does not have seccomp configuration.
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
			env := newServiceIntegrationEnv(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			artifact, result := compileProgram(t, env, tt.language, sourceCode)
			require.True(t, result.Succeeded, "compilation should succeed")

			runOut := runUserProgram(t, env, artifact, tt.language, "", 2000, 256)
			assert.Contains(t, []sandbox.Verdict{sandbox.VerdictRE, sandbox.VerdictOK},
				runOut.Verdict, "expected RE or sandbox failure for policy violation")
		})
	}
}
