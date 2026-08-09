package service

import (
	"context"
	"errors"
	"testing"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLanguageExecutor struct {
	compileResult execution.CompileResult
	compileErr    error
	runResult     execution.RunResult
}

func (e *fakeLanguageExecutor) Compile(
	_ context.Context,
	_ execution.CompileRequest,
) (execution.CompileResult, error) {
	return e.compileResult, e.compileErr
}

func (e *fakeLanguageExecutor) Run(
	_ context.Context,
	_ execution.RunRequest,
) (execution.RunResult, error) {
	return e.runResult, nil
}

func TestLanguageResolveRejectsUnsupportedLanguage(t *testing.T) {
	_, err := newLanguage(&fakeLanguageExecutor{}).Resolve(model.Language("Rust"))
	require.EqualError(t, err, "unsupported language: Rust")
}

func TestLanguageCompileOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		output      execution.CompileResult
		compileErr  error
		wantResult  model.CompileResult
		wantErr     string
		wantProgram bool
	}{
		{
			name:       "compile error",
			output:     execution.CompileResult{Log: "syntax error"},
			wantResult: model.CompileResult{Succeeded: false, Log: "syntax error"},
		},
		{
			name:       "infrastructure error",
			compileErr: errors.New("sandbox unavailable"),
			wantErr:    "sandbox unavailable",
		},
		{
			name: "successful compile",
			output: execution.CompileResult{
				Log:      "warning",
				Artifact: &execution.Artifact{Name: "program", Data: []byte("program"), Mode: 0o755},
			},
			wantResult:  model.CompileResult{Succeeded: true, Log: "warning"},
			wantProgram: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &fakeLanguageExecutor{compileResult: tt.output, compileErr: tt.compileErr}
			languageCompiler, err := newLanguage(executor).Resolve(model.LanguageCPP)
			require.NoError(t, err)

			program, result, err := languageCompiler.Compile(t.Context(), "source")
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Nil(t, program)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantResult, result)
			assert.Equal(t, tt.wantProgram, program != nil)
		})
	}
}

func TestLanguageRunNormalizesJavaOutOfMemory(t *testing.T) {
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
			stderr:      "java.lang.NullPointerException",
			wantVerdict: execution.VerdictRE,
		},
		{
			name:        "other languages are unchanged",
			language:    model.LanguageCPP,
			verdict:     execution.VerdictRE,
			stderr:      "java.lang.OutOfMemoryError",
			wantVerdict: execution.VerdictRE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &fakeLanguageExecutor{
				compileResult: execution.CompileResult{
					Artifact: &execution.Artifact{Name: "program", Data: []byte("program"), Mode: 0o755},
				},
				runResult: execution.RunResult{Verdict: tt.verdict, Stderr: tt.stderr},
			}
			languageCompiler, err := newLanguage(executor).Resolve(tt.language)
			require.NoError(t, err)
			program, _, err := languageCompiler.Compile(t.Context(), "source")
			require.NoError(t, err)

			result, err := program.Run(t.Context(), "", 1000, 128)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVerdict, result.Verdict)
		})
	}
}
