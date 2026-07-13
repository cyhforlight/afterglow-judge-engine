package service

import (
	"strings"
	"testing"
	"time"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runUserProgram(t *testing.T, env serviceIntegrationEnv, artifact *model.CompiledArtifact, lang model.Language, input string, timeLimit, memoryLimit int) RunResult {
	t.Helper()

	profile, err := ProfileForLanguage(lang)
	require.NoError(t, err)

	containerPath := runMountDir + "/" + profile.Run.ArtifactName
	runOut, err := env.runner.Run(env.ctx, RunRequest{
		Files: []workspace.File{{
			Name:    profile.Run.ArtifactName,
			Content: artifact.Data,
			Mode:    artifact.Mode,
		}},
		ImageRef: profile.Run.ImageRef,
		Command:  profile.Run.RuntimeCommand(containerPath, memoryLimit),
		Stdin:    strings.NewReader(input),
		Limits: execution.Limits{
			CPUTimeMs:   timeLimit,
			WallTimeMs:  timeLimit * execution.WallTimeMultiplier,
			MemoryMB:    sandboxMemoryLimitMB(lang, memoryLimit),
			OutputBytes: execution.DefaultRunOutputLimitBytes,
		},
	})
	require.NoError(t, err)
	return normalizeUserRunResult(lang, runOut)
}

type sandboxFailureCase struct {
	name     string
	language model.Language
	filePath string
}

func testProgramVerdicts(
	t *testing.T,
	tests []sandboxFailureCase,
	timeLimit, memoryLimit int,
	want execution.Verdict,
) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newServiceIntegrationEnv(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			artifact, result := compileProgram(t, env, tt.language, sourceCode)
			require.True(t, result.Succeeded, "compilation should succeed")

			runOut := runUserProgram(t, env, artifact, tt.language, "", timeLimit, memoryLimit)
			assert.Equal(t, want, runOut.Verdict)
		})
	}
}

func TestSandboxFailure_CompileError(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []sandboxFailureCase{
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

func TestSandboxFailure_TimeLimit(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []sandboxFailureCase{
		{"C infinite loop", model.LanguageC, "tle/tle_infinite_loop.c"},
		{"C++ infinite loop", model.LanguageCPP, "tle/tle_infinite_loop.cpp"},
		{"Java infinite loop", model.LanguageJava, "tle/tle_infinite_loop.java"},
		{"Python infinite loop", model.LanguagePython, "tle/tle_infinite_loop.py"},
	}

	testProgramVerdicts(t, tests, 1000, 256, execution.VerdictTLE)
}

func TestSandboxFailure_MemoryLimit(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []sandboxFailureCase{
		{"C malloc blocks", model.LanguageC, "mle/mle_malloc_blocks.c"},
		{"C++ vector push", model.LanguageCPP, "mle/mle_vector_push.cpp"},
		{"Java ArrayList", model.LanguageJava, "mle/mle_array_list.java"},
		{"Python list append", model.LanguagePython, "mle/mle_list_append.py"},
	}

	testProgramVerdicts(t, tests, 2000, 64, execution.VerdictMLE)
}

func TestJavaHeapMatchesRequestedMemoryLimit(t *testing.T) {
	requireServiceIntegrationTest(t)
	env := newServiceIntegrationEnv(t, 120*time.Second)

	const sourceCode = `
public class Main {
    public static void main(String[] args) {
        byte[] data = new byte[64 * 1024 * 1024];
        data[data.length - 1] = 1;
        System.out.println(data.length);
    }
}
`
	artifact, result := compileProgram(t, env, model.LanguageJava, sourceCode)
	require.True(t, result.Succeeded, "compilation should succeed")

	runOut := runUserProgram(t, env, artifact, model.LanguageJava, "", 2000, 128)
	require.Equal(t, execution.VerdictOK, runOut.Verdict, "execution failed: %s", runOut.ExtraInfo)
	assert.Equal(t, "67108864\n", runOut.Stdout)
}

func TestSandboxFailure_RuntimeError(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []sandboxFailureCase{
		{"C abort", model.LanguageC, "re/re_abort.c"},
		{"C++ segfault", model.LanguageCPP, "re/re_segfault.cpp"},
		{"C++ vector at", model.LanguageCPP, "re/re_vector_at.cpp"},
		{"C++ null dereference", model.LanguageCPP, "re/re_null_dereference.cpp"},
		{"Java null pointer", model.LanguageJava, "re/re_null_pointer.java"},
		{"Python index error", model.LanguagePython, "re/re_index_error.py"},
	}

	testProgramVerdicts(t, tests, 2000, 256, execution.VerdictRE)
}

func TestSandboxFailure_OutputLimit(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []sandboxFailureCase{
		{"C++ infinite output", model.LanguageCPP, "ole/ole_infinite_output.cpp"},
		{"Python infinite print", model.LanguagePython, "ole/ole_infinite_print.py"},
	}

	testProgramVerdicts(t, tests, 2000, 256, execution.VerdictOLE)
}

func TestSandboxFailure_PolicyViolation(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []sandboxFailureCase{
		{"C network socket", model.LanguageC, "policy/policy_network_socket.c"},
		{"Python network socket", model.LanguagePython, "policy/policy_network_socket.py"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceIntegrationEnv(t, 120*time.Second)
			sourceCode := readTestdata(t, "sandbox-failure-cases", tt.filePath)

			artifact, result := compileProgram(t, env, tt.language, sourceCode)
			require.True(t, result.Succeeded, "compilation should succeed")

			runOut := runUserProgram(t, env, artifact, tt.language, "", 2000, 256)
			assert.Equal(t, execution.VerdictOK, runOut.Verdict)
			assert.Contains(t, runOut.Stdout, "blocked")
		})
	}
}
