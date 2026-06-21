package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/workspace"
)

const compileMountDir = "/work"

// CompileRequest contains a generic compilation job definition.
type CompileRequest struct {
	Files        []workspace.File
	ImageRef     string
	Command      []string
	ArtifactName string
	Limits       execution.Limits
}

// CompileOutput is the generic compiler output.
type CompileOutput struct {
	Result   model.CompileResult
	Artifact *model.CompiledArtifact
}

// Compiler compiles a file set into an artifact.
type Compiler interface {
	Compile(ctx context.Context, req CompileRequest) (CompileOutput, error)
}

// compiler compiles files inside containers.
type compiler struct {
	executor execution.Executor
}

// NewCompiler creates a generic compiler primitive without caching.
// Use NewCachedCompiler to add caching capability.
func NewCompiler(executor execution.Executor) Compiler {
	return &compiler{
		executor: executor,
	}
}

// Compile compiles files in an isolated container.
func (c *compiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	var out CompileOutput

	if err := validateCompileRequest(req); err != nil {
		return out, err
	}

	out, err := c.compileInContainer(ctx, req)
	if err != nil {
		return out, err
	}
	if !out.Result.Succeeded {
		return out, nil
	}
	if out.Artifact == nil {
		return out, errors.New("compile succeeded but artifact is missing")
	}

	return out, nil
}

func validateCompileRequest(req CompileRequest) error {
	if strings.TrimSpace(req.ImageRef) == "" {
		return errors.New("compile image is required")
	}
	if len(req.Command) == 0 {
		return errors.New("compile command is required")
	}
	if len(req.Files) == 0 {
		return errors.New("at least one compile file is required")
	}
	if strings.TrimSpace(req.ArtifactName) == "" {
		return errors.New("artifact name is required")
	}
	return nil
}

func (c *compiler) compileInContainer(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	var out CompileOutput

	result, err := c.executor.Execute(ctx, execution.Job{
		Files:         req.Files,
		ImageRef:      req.ImageRef,
		Command:       req.Command,
		MountPath:     compileMountDir,
		ReadOnlyMount: false,
		Limits:        req.Limits,
		EnableSeccomp: false, // Compilation needs fork for shell scripts
		Artifacts:     []execution.ArtifactSpec{{Name: req.ArtifactName}},
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

	artifact, err := compiledArtifactFromResult(result, req.ArtifactName)
	if err != nil {
		return out, err
	}
	out.Artifact = &artifact
	return out, nil
}

func compiledArtifactFromResult(result execution.Result, name string) (model.CompiledArtifact, error) {
	artifact, ok := result.Artifacts[name]
	if !ok {
		return model.CompiledArtifact{}, fmt.Errorf("compiled artifact %q was not collected", name)
	}

	return model.CompiledArtifact{
		Data: artifact.Data,
		Mode: artifact.Mode,
	}, nil
}
