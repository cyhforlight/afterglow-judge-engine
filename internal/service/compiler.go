package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
	"afterglow-judge-sandbox/internal/storage"
	"afterglow-judge-sandbox/internal/workspace"
)

// CompileRequest contains source data for compilation.
type CompileRequest struct {
	Language   model.Language
	SourceCode string
}

// CompileOutput is the compiler output consumed by the judge service.
type CompileOutput struct {
	Result          model.CompileResult
	Artifact        *model.CompiledArtifact
	RuntimeLanguage model.Language
}

// Compiler compiles source code to a runnable artifact.
type Compiler interface {
	Compile(ctx context.Context, req CompileRequest) (CompileOutput, error)
}

// compiler compiles user source code inside containers.
type compiler struct {
	sandbox sandbox.Sandbox
	cache   storage.Storage
}

// NewCompiler creates a compiler.
func NewCompiler(sb sandbox.Sandbox, cacheStorage storage.Storage) Compiler {
	return &compiler{
		sandbox: sb,
		cache:   cacheStorage,
	}
}

// compileKey generates a cache key for compilation.
func compileKey(sourceCode string, lang model.Language, imageRef string, buildCommand []string) string {
	h := sha256.New()
	h.Write([]byte(sourceCode))
	h.Write([]byte(lang.String()))
	h.Write([]byte(imageRef))
	h.Write([]byte(strings.Join(buildCommand, "\x00")))
	return hex.EncodeToString(h.Sum(nil))
}

// Compile compiles source code in an isolated container.
func (c *compiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	var out CompileOutput

	profile, err := ProfileForLanguage(req.Language)
	if err != nil {
		return out, fmt.Errorf("get language profile: %w", err)
	}

	out.RuntimeLanguage = req.Language
	cacheKey := compileKey(
		req.SourceCode,
		req.Language,
		profile.Compile.ImageRef,
		profile.Compile.BuildCommand("/work", profile.Compile.SourceFiles),
	)

	// Try cache - Get() returns content directly ([]byte)
	if c.cache != nil {
		if data, err := c.cache.Get(ctx, cacheKey); err == nil {
			slog.InfoContext(ctx, "compilation cache hit", "key", cacheKey[:16])
			return CompileOutput{
				Result: model.CompileResult{Succeeded: true, Log: ""},
				Artifact: &model.CompiledArtifact{
					Name: profile.Compile.ArtifactName,
					Data: data,
					Mode: 0o755,
				},
				RuntimeLanguage: req.Language,
			}, nil
		}
		slog.InfoContext(ctx, "compilation cache miss", "key", cacheKey[:16])
	}

	// Cache miss, compile in container
	out, err = c.compileInContainer(ctx, req, profile)
	if err != nil {
		return out, err
	}

	// Compilation failed, return immediately
	if !out.Result.Succeeded {
		return out, nil
	}

	if out.Artifact == nil {
		return out, errors.New("compile succeeded but artifact is missing")
	}

	// Store in cache - StoreWithKey() accepts []byte directly
	if c.cache != nil {
		if err := c.cache.StoreWithKey(ctx, cacheKey, out.Artifact.Data); err != nil {
			slog.WarnContext(ctx, "failed to cache compilation artifact", "error", err)
		}
	}

	return out, nil
}

//nolint:funlen // Compilation requires setup, execution, and artifact handling
func (c *compiler) compileInContainer(
	ctx context.Context,
	req CompileRequest,
	profile LanguageProfile,
) (CompileOutput, error) {
	var out CompileOutput
	out.RuntimeLanguage = req.Language

	ws, err := workspace.New()
	if err != nil {
		return out, fmt.Errorf("create workspace: %w", err)
	}
	defer func() { _ = ws.Cleanup() }()

	if err := ws.WriteFile(profile.Compile.SourceFiles[0], []byte(req.SourceCode), 0644); err != nil {
		return out, fmt.Errorf("write source file: %w", err)
	}

	compileReq := sandbox.ExecuteRequest{
		ImageRef: profile.Compile.ImageRef,
		Command:  profile.Compile.BuildCommand(ws.Dir(), profile.Compile.SourceFiles),
		MountDir: &sandbox.Mount{
			HostPath:      ws.Dir(),
			ContainerPath: "/work",
			ReadOnly:      false,
		},
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	}

	result, err := c.sandbox.Execute(ctx, compileReq)
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

	// For Python, py_compile creates __pycache__/solution.cpython-311.pyc
	// We need to find and use the actual .pyc file
	artifactPath := filepath.Join(ws.Dir(), profile.Compile.ArtifactName)
	if req.Language == model.LanguagePython {
		// Python bytecode is in __pycache__/
		pycachePath := filepath.Join(ws.Dir(), "__pycache__")
		entries, err := os.ReadDir(pycachePath)
		if err == nil && len(entries) > 0 {
			// Use the first .pyc file found
			for _, entry := range entries {
				if filepath.Ext(entry.Name()) == ".pyc" {
					artifactPath = filepath.Join(pycachePath, entry.Name())
					break
				}
			}
		}
	}

	artifact, err := loadCompiledArtifact(artifactPath)
	if err != nil {
		return out, fmt.Errorf("read compiled artifact: %w", err)
	}

	out.Artifact = &artifact
	return out, nil
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
