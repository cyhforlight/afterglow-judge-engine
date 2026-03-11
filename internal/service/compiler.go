package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"
)

const compileMountDir = "/work"

// CompileFile is a single source or support file written into the compile workspace.
type CompileFile struct {
	Name    string
	Content []byte
	Mode    os.FileMode
}

// ArtifactLoader resolves the compiled artifact from the workspace.
type ArtifactLoader func(workDir string) (model.CompiledArtifact, error)

// CompileRequest contains a generic compilation job definition.
type CompileRequest struct {
	Files          []CompileFile
	ImageRef       string
	Command        []string
	ArtifactName   string
	ArtifactMode   os.FileMode
	ArtifactPath   string
	ArtifactLoader ArtifactLoader
	Limits         sandbox.ResourceLimits
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
	sandbox sandbox.Sandbox
}

// NewCompiler creates a generic compiler primitive without caching.
// Use NewCachedCompiler to add caching capability.
func NewCompiler(sb sandbox.Sandbox) Compiler {
	return &compiler{
		sandbox: sb,
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
	if strings.TrimSpace(req.ArtifactPath) == "" && req.ArtifactLoader == nil {
		return errors.New("artifact path or loader is required")
	}
	return nil
}

//nolint:funlen // Compilation requires setup, execution, and artifact handling.
func (c *compiler) compileInContainer(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	var out CompileOutput

	ws, err := workspace.New()
	if err != nil {
		return out, fmt.Errorf("create workspace: %w", err)
	}
	defer func() { _ = ws.Cleanup() }()

	for _, file := range req.Files {
		fileMode := file.Mode
		if fileMode == 0 {
			fileMode = 0644
		}
		if err := ws.WriteFile(file.Name, file.Content, fileMode); err != nil {
			return out, fmt.Errorf("write compile file %q: %w", file.Name, err)
		}
	}

	result, err := c.sandbox.Execute(ctx, sandbox.ExecuteRequest{
		ImageRef: req.ImageRef,
		Command:  req.Command,
		MountDir: &sandbox.Mount{
			HostPath:      ws.Dir(),
			ContainerPath: compileMountDir,
			ReadOnly:      false,
		},
		Limits: req.Limits,
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

	if result.ExitCode != 0 || result.Verdict != sandbox.VerdictOK {
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

	artifact, err := loadCompiledArtifactFromRequest(ws.Dir(), req)
	if err != nil {
		return out, fmt.Errorf("read compiled artifact: %w", err)
	}
	out.Artifact = &artifact
	return out, nil
}

func loadCompiledArtifactFromRequest(workDir string, req CompileRequest) (model.CompiledArtifact, error) {
	if req.ArtifactLoader != nil {
		artifact, err := req.ArtifactLoader(workDir)
		if err != nil {
			return model.CompiledArtifact{}, err
		}
		if artifact.Name == "" {
			artifact.Name = req.ArtifactName
		}
		if artifact.Mode == 0 {
			artifact.Mode = req.ArtifactMode
		}
		return artifact, nil
	}

	artifact, err := loadCompiledArtifact(filepath.Join(workDir, req.ArtifactPath))
	if err != nil {
		return model.CompiledArtifact{}, err
	}
	if artifact.Name == "" {
		artifact.Name = req.ArtifactName
	}
	if artifact.Mode == 0 {
		artifact.Mode = req.ArtifactMode
	}
	return artifact, nil
}

func loadCompiledArtifact(path string) (model.CompiledArtifact, error) {
	info, err := os.Stat(path)
	if err != nil {
		return model.CompiledArtifact{}, fmt.Errorf("stat artifact %q: %w", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return model.CompiledArtifact{}, fmt.Errorf("read artifact %q: %w", path, err)
	}

	return model.CompiledArtifact{
		Name: filepath.Base(path),
		Data: data,
		Mode: info.Mode().Perm(),
	}, nil
}
