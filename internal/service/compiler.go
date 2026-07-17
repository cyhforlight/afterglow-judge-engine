package service

import (
	"context"
	"fmt"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"
)

// CompileRequest contains a generic compilation job definition.
type CompileRequest struct {
	Files        []execution.File
	ImageRef     string
	Command      []string
	ArtifactName string
	Limits       execution.Limits
}

// CompileOutput is the generic compiler output.
// A successful result always includes an artifact.
type CompileOutput struct {
	Result   model.CompileResult
	Artifact *execution.Artifact
}

// Compiler compiles a file set into an artifact.
type Compiler interface {
	Compile(ctx context.Context, req CompileRequest) (CompileOutput, error)
}

// compiler compiles files inside containers.
type compiler struct {
	executor execution.Executor
}

func newCompiler(executor execution.Executor) Compiler {
	return &compiler{
		executor: executor,
	}
}

// Compile compiles files in an isolated container.
func (c *compiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	var out CompileOutput

	result, err := c.executor.Execute(ctx, execution.Job{
		Files:         req.Files,
		ImageRef:      req.ImageRef,
		Command:       req.Command,
		MountPath:     "/work",
		ReadOnlyMount: false,
		Limits:        req.Limits,
		EnableSeccomp: false, // Compilation needs fork for shell scripts
		Artifacts:     []string{req.ArtifactName},
	})
	if err != nil {
		return out, fmt.Errorf("execute compilation: %w", err)
	}

	compileLog := result.Stdout
	if result.Stderr != "" {
		if compileLog != "" {
			compileLog += "\n"
		}
		compileLog += result.Stderr
	}

	if result.ExitCode != 0 || result.Verdict != execution.VerdictOK {
		out.Result = model.CompileResult{
			Succeeded: false,
			Log:       compileLog,
		}
		return out, nil
	}

	out.Result = model.CompileResult{
		Succeeded: true,
		Log:       compileLog,
	}

	artifact := result.Artifacts[req.ArtifactName]
	out.Artifact = &artifact
	return out, nil
}
