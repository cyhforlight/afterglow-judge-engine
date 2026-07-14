package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

func TestLanguageCompileAndRunRequests(t *testing.T) {
	tests := []struct {
		name               string
		language           model.Language
		compileImage       string
		sourceFile         string
		artifactName       string
		compileCommand     []string
		compileTimeLimit   int
		compileMemory      int
		runImage           string
		runCommand         []string
		sandboxMemoryLimit int
	}{
		{
			name:               "C",
			language:           model.LanguageC,
			compileImage:       "docker.io/library/gcc:12-bookworm",
			sourceFile:         "main.c",
			artifactName:       "program",
			compileCommand:     []string{"gcc", "-O2", "-pipe", "-static", "-s", "-o", "/work/program", "/work/main.c", "-lm"},
			compileTimeLimit:   30000,
			compileMemory:      512,
			runImage:           "docker.io/library/debian:12-slim",
			runCommand:         []string{"/sandbox/program"},
			sandboxMemoryLimit: 128,
		},
		{
			name:               "C++",
			language:           model.LanguageCPP,
			compileImage:       "docker.io/library/gcc:12-bookworm",
			sourceFile:         "main.cpp",
			artifactName:       "program",
			compileCommand:     []string{"g++", "-std=c++20", "-O2", "-pipe", "-static", "-s", "-o", "/work/program", "/work/main.cpp", "-lm"},
			compileTimeLimit:   30000,
			compileMemory:      512,
			runImage:           "docker.io/library/debian:12-slim",
			runCommand:         []string{"/sandbox/program"},
			sandboxMemoryLimit: 128,
		},
		{
			name:         "Java",
			language:     model.LanguageJava,
			compileImage: "docker.io/library/eclipse-temurin:21-jdk-jammy",
			sourceFile:   "Main.java",
			artifactName: "solution.jar",
			compileCommand: []string{
				"sh", "-c",
				"mkdir -p /work/classes && javac -encoding UTF-8 -d /work/classes /work/Main.java && jar --create --file /work/solution.jar --main-class Main -C /work/classes .",
			},
			compileTimeLimit:   30000,
			compileMemory:      512,
			runImage:           "docker.io/library/eclipse-temurin:21-jre-jammy",
			runCommand:         []string{"java", "-Xmx128m", "-Xms64m", "-jar", "/sandbox/solution.jar"},
			sandboxMemoryLimit: 192,
		},
		{
			name:         "Python",
			language:     model.LanguagePython,
			compileImage: "docker.io/library/python:3.11-slim-bookworm",
			sourceFile:   "solution.py",
			artifactName: "solution.pyc",
			compileCommand: []string{
				"sh", "-c",
				"python3 -c 'import py_compile; py_compile.compile(\"/work/solution.py\", cfile=\"/work/solution.pyc\", doraise=True)' || exit 1",
			},
			compileTimeLimit:   10000,
			compileMemory:      256,
			runImage:           "docker.io/library/python:3.11-slim-bookworm",
			runCommand:         []string{"python3", "/sandbox/solution.pyc"},
			sandboxMemoryLimit: 128,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiler := &recordingCompiler{output: CompileOutput{
				Result:   model.CompileResult{Succeeded: true},
				Artifact: &execution.Artifact{Data: []byte("program"), Mode: 0o755},
			}}
			runner := &recordingRunner{result: RunResult{Verdict: execution.VerdictOK}}

			toolchain, err := newLanguage(compiler, runner).Resolve(tt.language)
			require.NoError(t, err)
			program, compileResult, err := toolchain.Compile(t.Context(), "source code")
			require.NoError(t, err)
			require.True(t, compileResult.Succeeded)

			require.Len(t, compiler.requests, 1)
			compileReq := compiler.requests[0]
			assert.Equal(t, tt.compileImage, compileReq.ImageRef)
			assert.Equal(t, tt.compileCommand, compileReq.Command)
			assert.Equal(t, tt.artifactName, compileReq.ArtifactName)
			require.Len(t, compileReq.Files, 1)
			assert.Equal(t, tt.sourceFile, compileReq.Files[0].Name)
			assert.Equal(t, []byte("source code"), compileReq.Files[0].Content)
			assert.Equal(t, fs.FileMode(0o644), compileReq.Files[0].Mode)
			assert.Equal(t, tt.compileTimeLimit, compileReq.Limits.CPUTimeMs)
			assert.Equal(t, tt.compileTimeLimit*execution.WallTimeMultiplier, compileReq.Limits.WallTimeMs)
			assert.Equal(t, tt.compileMemory, compileReq.Limits.MemoryMB)
			assert.Equal(t, int64(execution.DefaultCompileOutputLimitBytes), compileReq.Limits.OutputBytes)

			_, err = program.Run(t.Context(), "input data", 250, 128)
			require.NoError(t, err)
			require.Len(t, runner.requests, 1)
			runReq := runner.requests[0]
			assert.Equal(t, "input data", runReq.input)
			assert.Equal(t, tt.runImage, runReq.request.ImageRef)
			assert.Equal(t, tt.runCommand, runReq.request.Command)
			require.Len(t, runReq.request.Files, 1)
			assert.Equal(t, tt.artifactName, runReq.request.Files[0].Name)
			assert.Equal(t, []byte("program"), runReq.request.Files[0].Content)
			assert.Equal(t, fs.FileMode(0o755), runReq.request.Files[0].Mode)
			assert.Equal(t, 250, runReq.request.Limits.CPUTimeMs)
			assert.Equal(t, 250*execution.WallTimeMultiplier, runReq.request.Limits.WallTimeMs)
			assert.Equal(t, tt.sandboxMemoryLimit, runReq.request.Limits.MemoryMB)
			assert.Equal(t, int64(execution.DefaultRunOutputLimitBytes), runReq.request.Limits.OutputBytes)
		})
	}
}

func TestLanguageResolveRejectsUnknownLanguage(t *testing.T) {
	_, err := newLanguage(&recordingCompiler{}, &recordingRunner{}).Resolve(model.LanguageUnknown)
	require.EqualError(t, err, "unsupported language: Unknown")
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
			name:    "successful compile requires artifact",
			output:  CompileOutput{Result: model.CompileResult{Succeeded: true}},
			wantErr: "compile succeeded but artifact is missing",
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
			toolchain, err := newLanguage(compiler, &recordingRunner{}).Resolve(model.LanguageCPP)
			require.NoError(t, err)

			program, result, err := toolchain.Compile(t.Context(), "source")
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
			toolchain, err := newLanguage(compiler, runner).Resolve(tt.language)
			require.NoError(t, err)
			program, _, err := toolchain.Compile(t.Context(), "source")
			require.NoError(t, err)

			result, err := program.Run(t.Context(), "", 1000, 128)
			require.NoError(t, err)
			assert.Equal(t, tt.wantVerdict, result.Verdict)
		})
	}
}

func TestCompiledProgramRejectsEmptyArtifactAndPropagatesRunnerError(t *testing.T) {
	tests := []struct {
		name      string
		artifact  *execution.Artifact
		runnerErr error
		wantErr   string
	}{
		{
			name:     "empty artifact",
			artifact: &execution.Artifact{Mode: 0o755},
			wantErr:  "program artifact is required",
		},
		{
			name:      "runner error",
			artifact:  &execution.Artifact{Data: []byte("program"), Mode: 0o755},
			runnerErr: errors.New("sandbox unavailable"),
			wantErr:   "sandbox unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiler := &recordingCompiler{output: CompileOutput{
				Result:   model.CompileResult{Succeeded: true},
				Artifact: tt.artifact,
			}}
			runner := &recordingRunner{err: tt.runnerErr}
			toolchain, err := newLanguage(compiler, runner).Resolve(model.LanguageCPP)
			require.NoError(t, err)
			program, _, err := toolchain.Compile(t.Context(), "source")
			require.NoError(t, err)

			_, err = program.Run(t.Context(), "", 1000, 128)
			require.EqualError(t, err, tt.wantErr)
		})
	}
}

func TestCompiledProgramSupportsConcurrentRuns(t *testing.T) {
	compiler := &recordingCompiler{output: CompileOutput{
		Result:   model.CompileResult{Succeeded: true},
		Artifact: &execution.Artifact{Data: []byte("program"), Mode: 0o755},
	}}
	runner := &recordingRunner{result: RunResult{Verdict: execution.VerdictOK}}
	toolchain, err := newLanguage(compiler, runner).Resolve(model.LanguageCPP)
	require.NoError(t, err)
	program, _, err := toolchain.Compile(t.Context(), "source")
	require.NoError(t, err)

	const runCount = 20
	errs := make([]error, runCount)
	var wg sync.WaitGroup
	for i := range runCount {
		wg.Go(func() {
			errs[i] = func() error {
				_, err := program.Run(t.Context(), fmt.Sprintf("input-%d", i), 1000, 128)
				return err
			}()
		})
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Len(t, runner.requests, runCount)
}
