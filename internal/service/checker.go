package service

import (
	"context"
	"errors"
	"strings"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
)

const (
	defaultCheckerSourceKey = "checkers/default.cpp"
	testlibHeaderKey        = "testlib.h"

	checkerSourceFileName   = "checker.cpp"
	checkerArtifactFileName = "checker"
	checkerInputFileName    = "input.txt"
	checkerOutputFileName   = "output.txt"
	checkerAnswerFileName   = "answer.txt"

	checkerCPUTimeLimitMs = 3000
	checkerMemoryLimitMB  = 256
)

// ResourceStore provides read-only access to internal checker resources.
type ResourceStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// CheckerCompileRequest contains the source files required to build a checker.
type CheckerCompileRequest struct {
	SourceCode   []byte
	SupportFiles []CompileFile
}

// CheckerCompileOutput contains the compiled checker artifact.
type CheckerCompileOutput struct {
	Result   model.CompileResult
	Artifact *model.CompiledArtifact
}

// CheckerCompiler compiles checker source code into a runnable artifact.
type CheckerCompiler interface {
	Compile(ctx context.Context, req CheckerCompileRequest) (CheckerCompileOutput, error)
}

type checkerCompiler struct {
	compiler Compiler
}

// NewCheckerCompiler creates a checker compiler.
func NewCheckerCompiler(compiler Compiler) CheckerCompiler {
	return &checkerCompiler{compiler: compiler}
}

// Compile compiles a checker with its bundled support files.
func (c *checkerCompiler) Compile(ctx context.Context, req CheckerCompileRequest) (CheckerCompileOutput, error) {
	if len(req.SourceCode) == 0 {
		return CheckerCompileOutput{}, errors.New("checker source code is required")
	}

	profile := cppProfile()
	files := make([]CompileFile, 0, 1+len(req.SupportFiles))
	files = append(files, CompileFile{
		Name:    checkerSourceFileName,
		Content: req.SourceCode,
		Mode:    0o644,
	})
	files = append(files, req.SupportFiles...)

	out, err := c.compiler.Compile(ctx, CompileRequest{
		Files:        files,
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand(compileMountDir, []string{checkerSourceFileName}),
		ArtifactName: checkerArtifactFileName,
		ArtifactMode: profile.Run.FileMode,
		ArtifactPath: profile.Compile.ArtifactName,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	})
	if err != nil {
		return CheckerCompileOutput{}, err
	}
	if out.Artifact != nil {
		out.Artifact.Name = checkerArtifactFileName
	}

	return CheckerCompileOutput(out), nil
}

// CheckerRunRequest contains the files required to compare one testcase.
type CheckerRunRequest struct {
	Checker        model.CompiledArtifact
	InputText      string
	ActualOutput   string
	ExpectedOutput string
}

// CheckerRunResult contains the checker outcome.
type CheckerRunResult struct {
	Verdict  model.Verdict // only OK / WA / UKE
	Message  string
	ExitCode int
}

// CheckerRunner executes a compiled checker.
type CheckerRunner interface {
	Run(ctx context.Context, req CheckerRunRequest) (CheckerRunResult, error)
}

type checkerRunner struct {
	runner Runner
}

// NewCheckerRunner creates a checker runner.
func NewCheckerRunner(runner Runner) CheckerRunner {
	return &checkerRunner{runner: runner}
}

// Run executes a checker against one testcase output pair.
func (r *checkerRunner) Run(ctx context.Context, req CheckerRunRequest) (CheckerRunResult, error) {
	if len(req.Checker.Data) == 0 {
		return CheckerRunResult{}, errors.New("checker artifact is required")
	}

	profile := cppProfile().Run
	checkerMode := req.Checker.Mode
	if checkerMode == 0 {
		checkerMode = profile.FileMode
	}

	runOut, err := r.runner.Run(ctx, RunRequest{
		Files: []RunFile{
			{
				Name:    checkerArtifactFileName,
				Content: req.Checker.Data,
				Mode:    checkerMode,
			},
			{
				Name:    checkerInputFileName,
				Content: []byte(req.InputText),
				Mode:    0o644,
			},
			{
				Name:    checkerOutputFileName,
				Content: []byte(req.ActualOutput),
				Mode:    0o644,
			},
			{
				Name:    checkerAnswerFileName,
				Content: []byte(req.ExpectedOutput),
				Mode:    0o644,
			},
		},
		ImageRef: profile.ImageRef,
		Command: []string{
			runMountDir + "/" + checkerArtifactFileName,
			runMountDir + "/" + checkerInputFileName,
			runMountDir + "/" + checkerOutputFileName,
			runMountDir + "/" + checkerAnswerFileName,
		},
		Cwd:    runMountDir,
		Limits: checkerRunLimits(),
	})
	if err != nil {
		return CheckerRunResult{}, err
	}

	return convertCheckerRunResult(runOut), nil
}

func convertCheckerRunResult(runOut RunResult) CheckerRunResult {
	message := strings.TrimSpace(runOut.Stderr)
	if message == "" {
		message = strings.TrimSpace(runOut.Stdout)
	}
	if message == "" {
		message = strings.TrimSpace(runOut.ExtraInfo)
	}

	switch runOut.Verdict {
	case sandbox.VerdictTLE, sandbox.VerdictMLE, sandbox.VerdictOLE:
		return CheckerRunResult{
			Verdict:  model.VerdictUKE,
			Message:  message,
			ExitCode: runOut.ExitCode,
		}
	}

	switch runOut.ExitCode {
	case 0:
		if runOut.Verdict != sandbox.VerdictOK {
			return CheckerRunResult{
				Verdict:  model.VerdictUKE,
				Message:  message,
				ExitCode: runOut.ExitCode,
			}
		}
		return CheckerRunResult{
			Verdict:  model.VerdictOK,
			Message:  message,
			ExitCode: runOut.ExitCode,
		}
	case 1, 2:
		return CheckerRunResult{
			Verdict:  model.VerdictWA,
			Message:  message,
			ExitCode: runOut.ExitCode,
		}
	default:
		return CheckerRunResult{
			Verdict:  model.VerdictUKE,
			Message:  message,
			ExitCode: runOut.ExitCode,
		}
	}
}
