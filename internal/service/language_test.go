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

type recordingCompiler struct {
	mu       sync.Mutex
	output   CompileOutput
	err      error
	requests []CompileRequest
}

func (c *recordingCompiler) Compile(_ context.Context, req CompileRequest) (CompileOutput, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
	return c.output, c.err
}

type recordedRun struct {
	request RunRequest
	input   string
}

type recordingRunner struct {
	mu       sync.Mutex
	result   RunResult
	err      error
	requests []recordedRun
}

func (r *recordingRunner) Run(_ context.Context, req RunRequest) (RunResult, error) {
	input, err := io.ReadAll(req.Stdin)
	if err != nil {
		return RunResult{}, fmt.Errorf("read stdin: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, recordedRun{request: req, input: string(input)})
	return r.result, r.err
}

func TestLanguageResolveRejectsUnsupportedLanguage(t *testing.T) {
	_, err := newLanguage(&recordingCompiler{}, &recordingRunner{}).Resolve(model.Language("Rust"))
	require.EqualError(t, err, "unsupported language: Rust")
}

func TestLanguageCompileOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		output      CompileOutput
		compileErr  error
		wantResult  model.CompileResult
		wantErr     string
		wantProgram bool
	}{
		{
			name:       "compile error",
			output:     CompileOutput{Result: model.CompileResult{Succeeded: false, Log: "syntax error"}},
			wantResult: model.CompileResult{Succeeded: false, Log: "syntax error"},
		},
		{
			name:       "infrastructure error",
			compileErr: errors.New("sandbox unavailable"),
			wantErr:    "sandbox unavailable",
		},
		{
			name: "successful compile",
			output: CompileOutput{
				Result:   model.CompileResult{Succeeded: true, Log: "warning"},
				Artifact: &execution.Artifact{Data: []byte("program"), Mode: 0o755},
			},
			wantResult:  model.CompileResult{Succeeded: true, Log: "warning"},
			wantProgram: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiler := &recordingCompiler{output: tt.output, err: tt.compileErr}
			languageCompiler, err := newLanguage(compiler, &recordingRunner{}).Resolve(model.LanguageCPP)
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
			compiler := &recordingCompiler{output: CompileOutput{
				Result:   model.CompileResult{Succeeded: true},
				Artifact: &execution.Artifact{Data: []byte("program"), Mode: 0o755},
			}}
			runner := &recordingRunner{result: RunResult{Verdict: tt.verdict, Stderr: tt.stderr}}
			languageCompiler, err := newLanguage(compiler, runner).Resolve(tt.language)
			require.NoError(t, err)
			program, _, err := languageCompiler.Compile(t.Context(), "source")
			require.NoError(t, err)

			result, err := program.Run(t.Context(), "", 1000, 128)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVerdict, result.Verdict)
		})
	}
}

func TestCompiledProgramPropagatesRunnerError(t *testing.T) {
	compiler := &recordingCompiler{output: CompileOutput{
		Result:   model.CompileResult{Succeeded: true},
		Artifact: &execution.Artifact{Data: []byte("program"), Mode: 0o755},
	}}
	runner := &recordingRunner{err: errors.New("sandbox unavailable")}
	languageCompiler, err := newLanguage(compiler, runner).Resolve(model.LanguageCPP)
	require.NoError(t, err)
	program, _, err := languageCompiler.Compile(t.Context(), "source")
	require.NoError(t, err)

	_, err = program.Run(t.Context(), "", 1000, 128)
	require.EqualError(t, err, "sandbox unavailable")
}

func TestCompiledProgramSupportsConcurrentRuns(t *testing.T) {
	compiler := &recordingCompiler{output: CompileOutput{
		Result:   model.CompileResult{Succeeded: true},
		Artifact: &execution.Artifact{Data: []byte("program"), Mode: 0o755},
	}}
	runner := &recordingRunner{result: RunResult{Verdict: execution.VerdictOK}}
	languageCompiler, err := newLanguage(compiler, runner).Resolve(model.LanguageCPP)
	require.NoError(t, err)
	program, _, err := languageCompiler.Compile(t.Context(), "source")
	require.NoError(t, err)

	const runCount = 20
	errs := make([]error, runCount)
	var wg sync.WaitGroup
	for i := range runCount {
		wg.Go(func() {
			_, errs[i] = program.Run(t.Context(), fmt.Sprintf("input-%d", i), 1000, 128)
		})
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Len(t, runner.requests, runCount)
}
