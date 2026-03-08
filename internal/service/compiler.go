package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"afterglow-judge-sandbox/internal/cache"
	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
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

// ContainerCompiler compiles user source code inside containers.
type ContainerCompiler struct {
	sandbox sandbox.Sandbox
	cache   *cache.CompileCache
}

// NewContainerCompiler creates a container-based compiler.
func NewContainerCompiler(sb sandbox.Sandbox, compileCache *cache.CompileCache) *ContainerCompiler {
	return &ContainerCompiler{
		sandbox: sb,
		cache:   compileCache,
	}
}

// Compile compiles source code in an isolated container.
func (c *ContainerCompiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	var out CompileOutput

	profile, err := sandbox.ProfileForLanguage(req.Language)
	if err != nil {
		return out, fmt.Errorf("get language profile: %w", err)
	}

	out.RuntimeLanguage = req.Language
	cacheKey := cache.CompileKey(req.SourceCode, req.Language, profile)

	// 1. Try to get from cache
	if c.cache != nil {
		if cachedOut, ok := c.tryGetFromCache(ctx, cacheKey); ok {
			return cachedOut, nil
		}
		slog.InfoContext(ctx, "compilation cache miss", "key", cacheKey[:16])
	}

	// 2. Cache miss, compile in container
	out, err = c.compileInContainer(ctx, req, profile)
	if err != nil {
		return out, err
	}

	// 3. Compilation failed, return immediately
	if !out.Result.Succeeded {
		return out, nil
	}

	if out.Artifact == nil {
		return out, errors.New("compile succeeded but artifact is missing")
	}
	if c.cache == nil {
		return out, nil
	}

	if err := c.cache.Put(cacheKey, *out.Artifact, out.Result.Log, out.RuntimeLanguage); err != nil {
		slog.WarnContext(ctx, "failed to cache compilation artifact", "error", err)
		return out, nil
	}

	cached, ok := c.cache.Get(cacheKey)
	if !ok {
		slog.WarnContext(ctx, "failed to read just-cached artifact", "key", cacheKey[:16])
		return out, nil
	}
	out.Artifact = &cached.Artifact

	return out, nil
}

// tryGetFromCache attempts to retrieve a cached artifact.
// Returns (output, true) on success, (empty, false) on cache miss or error.
func (c *ContainerCompiler) tryGetFromCache(
	ctx context.Context,
	cacheKey string,
) (CompileOutput, bool) {
	cached, ok := c.cache.Get(cacheKey)
	if !ok {
		return CompileOutput{}, false
	}

	slog.InfoContext(ctx, "compilation cache hit", "key", cacheKey[:16])

	return CompileOutput{
		Result:          model.CompileResult{Succeeded: true, Log: cached.CompileLog},
		Artifact:        &cached.Artifact,
		RuntimeLanguage: cached.Language,
	}, true
}

//nolint:funlen // Compilation requires setup, execution, and artifact handling
func (c *ContainerCompiler) compileInContainer(
	ctx context.Context,
	req CompileRequest,
	profile sandbox.LanguageProfile,
) (CompileOutput, error) {
	var out CompileOutput
	out.RuntimeLanguage = req.Language

	ws, err := NewWorkspace()
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
