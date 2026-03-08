package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
)

// UserCodeCompileRequest contains source data for user code compilation.
type UserCodeCompileRequest struct {
	Language   model.Language
	SourceCode string
}

// UserCodeCompileOutput is the compiler output consumed by the judge service.
type UserCodeCompileOutput struct {
	Result          model.CompileResult
	Artifact        *model.CompiledArtifact
	RuntimeLanguage model.Language
}

// UserCodeCompiler compiles user source code to a runnable artifact.
type UserCodeCompiler interface {
	Compile(ctx context.Context, req UserCodeCompileRequest) (UserCodeCompileOutput, error)
}

type userCodeCompiler struct {
	compiler Compiler
}

// NewUserCodeCompiler creates a user code compiler.
func NewUserCodeCompiler(compiler Compiler) UserCodeCompiler {
	return &userCodeCompiler{compiler: compiler}
}

// Compile compiles user source code in an isolated container.
func (c *userCodeCompiler) Compile(ctx context.Context, req UserCodeCompileRequest) (UserCodeCompileOutput, error) {
	var out UserCodeCompileOutput

	profile, err := ProfileForLanguage(req.Language)
	if err != nil {
		return out, fmt.Errorf("get language profile: %w", err)
	}

	out.RuntimeLanguage = req.Language

	compileReq := CompileRequest{
		Files: []CompileFile{{
			Name:    profile.Compile.SourceFiles[0],
			Content: []byte(req.SourceCode),
			Mode:    0644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand(compileMountDir, profile.Compile.SourceFiles),
		ArtifactName: profile.Compile.ArtifactName,
		ArtifactMode: profile.Run.FileMode,
		ArtifactPath: profile.Compile.ArtifactName,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	}
	if req.Language == model.LanguagePython {
		compileReq.ArtifactLoader = loadPythonBytecodeArtifact(profile.Compile.ArtifactName, profile.Run.FileMode)
	}

	compileOut, err := c.compiler.Compile(ctx, compileReq)
	if err != nil {
		return out, err
	}

	out.Result = compileOut.Result
	out.Artifact = compileOut.Artifact
	return out, nil
}

func loadPythonBytecodeArtifact(artifactName string, artifactMode os.FileMode) ArtifactLoader {
	return func(workDir string) (model.CompiledArtifact, error) {
		pycachePath := filepath.Join(workDir, "__pycache__")
		entries, err := os.ReadDir(pycachePath)
		if err != nil {
			return model.CompiledArtifact{}, fmt.Errorf("read python cache directory: %w", err)
		}

		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".pyc" {
				continue
			}

			artifact, err := loadCompiledArtifact(filepath.Join(pycachePath, entry.Name()))
			if err != nil {
				return model.CompiledArtifact{}, err
			}
			artifact.Name = artifactName
			if artifact.Mode == 0 {
				artifact.Mode = artifactMode
			}
			return artifact, nil
		}

		return model.CompiledArtifact{}, fmt.Errorf("python bytecode artifact not found in %q", pycachePath)
	}
}
