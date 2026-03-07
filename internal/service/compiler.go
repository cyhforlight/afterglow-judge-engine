package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/singleflight"

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
	ArtifactPath    string
	RuntimeLanguage model.Language
	Cleanup         func()
}

// Compiler compiles source code to a runnable artifact.
type Compiler interface {
	Compile(ctx context.Context, req CompileRequest) (CompileOutput, error)
}

// ContainerCompiler compiles user source code inside containers.
type ContainerCompiler struct {
	sandbox sandbox.Sandbox
	cache   *cache.CompileCache
	sfGroup singleflight.Group // prevents duplicate concurrent compilations
}

// NewContainerCompiler creates a container-based compiler.
func NewContainerCompiler(sb sandbox.Sandbox) *ContainerCompiler {
	cacheDir := filepath.Join(os.TempDir(), "afterglow-compile-cache")
	const maxEntries = 500 // ~250MB-2.5GB

	compileCache, err := cache.GetGlobalCache(cacheDir, maxEntries)
	if err != nil {
		slog.Warn("failed to initialize compile cache", "error", err)
	}

	return &ContainerCompiler{
		sandbox: sb,
		cache:   compileCache,
	}
}

// Compile compiles source code in an isolated container.
//
//nolint:funlen // Multiple code paths (cache hit, cache miss, cache failure) require length
func (c *ContainerCompiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	var out CompileOutput

	profile, err := sandbox.ProfileForLanguage(req.Language)
	if err != nil {
		return out, fmt.Errorf("get language profile: %w", err)
	}

	out.RuntimeLanguage = req.Language

	cacheKey := cache.CompileKey(req.SourceCode, req.Language, profile)

	// Check cache first
	if c.cache != nil {
		if cachedOut, ok := c.tryGetFromCache(ctx, cacheKey, profile); ok {
			return cachedOut, nil
		}
		slog.InfoContext(ctx, "compilation cache miss", "key", cacheKey[:16])
	}

	// Singleflight: compile once, optionally cache, return result
	result, err, shared := c.sfGroup.Do(cacheKey, func() (any, error) {
		return c.compileAndCache(ctx, req, profile, cacheKey)
	})

	if err != nil {
		// Compilation failure - return immediately with error details
		var compileErr *compilationError
		if errors.As(err, &compileErr) {
			return CompileOutput{
				Result:          model.CompileResult{Succeeded: false, Log: compileErr.log},
				RuntimeLanguage: req.Language,
				Cleanup:         func() {},
			}, nil
		}
		return CompileOutput{}, err
	}

	if shared {
		slog.InfoContext(ctx, "compilation shared via singleflight", "key", cacheKey[:16])
	}

	compileResult := result.(*compileResult)

	// If caching succeeded, all requests copy from cache
	if compileResult.cached && c.cache != nil {
		cachedOut, ok := c.tryGetFromCache(ctx, cacheKey, profile)
		if !ok {
			return CompileOutput{}, errors.New("failed to retrieve from cache after successful compilation")
		}
		return cachedOut, nil
	}

	// If caching failed, copy the artifact to request-local workspace
	// Use reference counting to ensure original workspace is only cleaned after all requests finish
	if compileResult.refCount != nil {
		atomic.AddInt32(compileResult.refCount, 1)
	}

	copied, err := c.copyArtifactToLocalWorkspace(ctx, compileResult.artifactPath, compileResult.compileLog, req.Language, profile)
	if err != nil {
		// Decrement reference count and cleanup if this was the last reference
		c.releaseCompileResult(compileResult)
		return CompileOutput{}, err
	}

	// Decrement reference count and cleanup if this was the last reference
	c.releaseCompileResult(compileResult)

	return copied, nil
}

// releaseCompileResult decrements the reference count and cleans up if this was the last reference.
func (c *ContainerCompiler) releaseCompileResult(result *compileResult) {
	if result.refCount == nil || result.refMu == nil {
		return
	}

	result.refMu.Lock()
	defer result.refMu.Unlock()

	newCount := atomic.AddInt32(result.refCount, -1)
	if newCount == 0 && result.cleanup != nil {
		result.cleanup()
	}
}

// compilationError wraps compilation failures (not infrastructure errors).
type compilationError struct {
	log string
}

func (e *compilationError) Error() string {
	return "compilation failed: " + e.log
}

// compileResult holds the result of a compilation attempt.
type compileResult struct {
	cached       bool        // whether the artifact was successfully cached
	artifactPath string      // path to the compiled artifact (valid only if !cached)
	compileLog   string      // compilation log
	cleanup      func()      // cleanup function for original workspace (valid only if !cached)
	refCount     *int32      // reference count for shared fallback artifacts
	refMu        *sync.Mutex // protects cleanup during reference counting
}

// compileAndCache compiles source code and attempts to store in cache.
// Returns compileResult indicating whether caching succeeded.
// If caching fails, the artifact and cleanup function are preserved for fallback copying.
func (c *ContainerCompiler) compileAndCache(
	ctx context.Context,
	req CompileRequest,
	profile sandbox.LanguageProfile,
	cacheKey string,
) (*compileResult, error) {
	out, err := c.compileInContainer(ctx, req, profile)
	if err != nil {
		return nil, err
	}

	// If compilation failed, return compilation error (not infrastructure error)
	if !out.Result.Succeeded {
		out.Cleanup() // Clean up immediately for failed compilations
		return nil, &compilationError{log: out.Result.Log}
	}

	// Try to store in cache
	if c.cache != nil {
		if err := c.cache.Put(cacheKey, out.ArtifactPath, out.Result.Log, out.RuntimeLanguage); err != nil {
			slog.WarnContext(ctx, "failed to cache compilation", "error", err)
			// Cache failed - return artifact with reference counting for concurrent access
			refCount := int32(0) // Start at 0; each request will increment before use
			return &compileResult{
				cached:       false,
				artifactPath: out.ArtifactPath,
				compileLog:   out.Result.Log,
				cleanup:      out.Cleanup,
				refCount:     &refCount,
				refMu:        &sync.Mutex{},
			}, nil
		}
		// Cache succeeded - clean up workspace
		out.Cleanup()
		return &compileResult{
			cached:     true,
			compileLog: out.Result.Log,
		}, nil
	}

	// Cache unavailable - return artifact with reference counting for concurrent access
	refCount := int32(0) // Start at 0; each request will increment before use
	return &compileResult{
		cached:       false,
		artifactPath: out.ArtifactPath,
		compileLog:   out.Result.Log,
		cleanup:      out.Cleanup,
		refCount:     &refCount,
		refMu:        &sync.Mutex{},
	}, nil
}

// copyArtifactToLocalWorkspace copies an artifact to a request-local workspace.
// Used as fallback when caching is unavailable or fails.
func (c *ContainerCompiler) copyArtifactToLocalWorkspace(
	_ context.Context,
	sourcePath string,
	compileLog string,
	lang model.Language,
	profile sandbox.LanguageProfile,
) (CompileOutput, error) {
	ws, err := sandbox.NewWorkspace()
	if err != nil {
		return CompileOutput{}, fmt.Errorf("create workspace for artifact copy: %w", err)
	}

	artifactName := profile.Compile.ArtifactName
	localPath := filepath.Join(ws.Dir(), artifactName)

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		_ = ws.Cleanup()
		return CompileOutput{}, fmt.Errorf("read source artifact: %w", err)
	}

	if err := os.WriteFile(localPath, data, 0644); err != nil {
		_ = ws.Cleanup()
		return CompileOutput{}, fmt.Errorf("write local artifact: %w", err)
	}

	return CompileOutput{
		Result:          model.CompileResult{Succeeded: true, Log: compileLog},
		ArtifactPath:    localPath,
		RuntimeLanguage: lang,
		Cleanup:         func() { _ = ws.Cleanup() },
	}, nil
}

// tryGetFromCache attempts to retrieve and copy a cached artifact.
// Returns (output, true) on success, (empty, false) on cache miss or error.
func (c *ContainerCompiler) tryGetFromCache(
	ctx context.Context,
	cacheKey string,
	profile sandbox.LanguageProfile,
) (CompileOutput, bool) {
	cached, ok := c.cache.Get(cacheKey)
	if !ok {
		return CompileOutput{}, false
	}

	slog.InfoContext(ctx, "compilation cache hit", "key", cacheKey[:16])

	// Copy cached artifact to request-local workspace
	ws, err := sandbox.NewWorkspace()
	if err != nil {
		slog.WarnContext(ctx, "failed to create workspace for cached artifact", "error", err)
		return CompileOutput{}, false
	}

	artifactName := profile.Compile.ArtifactName
	localPath := filepath.Join(ws.Dir(), artifactName)

	data, err := os.ReadFile(cached.ArtifactPath)
	if err != nil {
		slog.WarnContext(ctx, "failed to read cached artifact", "error", err)
		_ = ws.Cleanup()
		return CompileOutput{}, false
	}

	if err := os.WriteFile(localPath, data, 0644); err != nil {
		slog.WarnContext(ctx, "failed to copy cached artifact", "error", err)
		_ = ws.Cleanup()
		return CompileOutput{}, false
	}

	return CompileOutput{
		Result:          model.CompileResult{Succeeded: true, Log: cached.CompileLog},
		ArtifactPath:    localPath,
		RuntimeLanguage: cached.Language,
		Cleanup:         func() { _ = ws.Cleanup() },
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

	ws, err := sandbox.NewWorkspace()
	if err != nil {
		return out, fmt.Errorf("create workspace: %w", err)
	}

	out.Cleanup = func() { _ = ws.Cleanup() }

	if err := ws.WriteFile(profile.Compile.SourceFiles[0], []byte(req.SourceCode), 0644); err != nil {
		return out, fmt.Errorf("write source file: %w", err)
	}

	compileReq := sandbox.ExecuteRequest{
		ImageRef: profile.Compile.ImageRef,
		Command:  profile.Compile.BuildCommand(ws.Dir(), profile.Compile.SourceFiles),
		Mounts: []sandbox.Mount{{
			HostPath:      ws.Dir(),
			ContainerPath: "/work",
			ReadOnly:      false,
		}},
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * 3,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: 1024 * 1024, // 1MB compile output
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

	out.ArtifactPath = artifactPath
	return out, nil
}

// HostCompiler compiles user source code on the host machine (deprecated).
type HostCompiler struct{}

// NewHostCompiler creates a host compiler (deprecated).
func NewHostCompiler() *HostCompiler {
	return &HostCompiler{}
}

// Compile compiles source code based on language profile.
func (c *HostCompiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	var out CompileOutput

	workDir, err := os.MkdirTemp("", "judge-compile-*")
	if err != nil {
		return out, fmt.Errorf("create compile temp dir: %w", err)
	}

	out.Cleanup = func() { _ = os.RemoveAll(workDir) }

	sourcePath, artifactPath, compileLog, succeeded, err := compileByLanguage(ctx, workDir, req)
	if err != nil {
		var compileErr *compileFailureError
		if errors.As(err, &compileErr) {
			out.Result = model.CompileResult{Succeeded: false, Log: compileErr.log}
			out.RuntimeLanguage = req.Language
			out.ArtifactPath = ""
			return out, nil
		}
		out.Cleanup()
		return out, err
	}

	_ = sourcePath
	out.Result = model.CompileResult{Succeeded: succeeded, Log: compileLog}
	if !succeeded {
		out.ArtifactPath = ""
		out.RuntimeLanguage = req.Language
		return out, nil
	}

	out.ArtifactPath = artifactPath
	out.RuntimeLanguage = req.Language
	return out, nil
}

type compileFailureError struct {
	log string
}

func (e *compileFailureError) Error() string {
	return e.log
}

func compileByLanguage(
	ctx context.Context,
	workDir string,
	req CompileRequest,
) (sourcePath, artifactPath, compileLog string, succeeded bool, err error) {
	switch req.Language {
	case model.LanguagePython:
		sourcePath = filepath.Join(workDir, "solution.py")
		if err = os.WriteFile(sourcePath, []byte(req.SourceCode), 0o644); err != nil {
			return "", "", "", false, fmt.Errorf("write python source: %w", err)
		}
		return sourcePath, sourcePath, "python does not require compile", true, nil

	case model.LanguageC:
		sourcePath = filepath.Join(workDir, "main.c")
		artifactPath = filepath.Join(workDir, "program")
		return compileNative(ctx, req.SourceCode, sourcePath, artifactPath, "gcc", []string{"-O2", "-pipe", "-static", "-s"})

	case model.LanguageCPP:
		sourcePath = filepath.Join(workDir, "main.cpp")
		artifactPath = filepath.Join(workDir, "program")
		return compileNative(ctx, req.SourceCode, sourcePath, artifactPath, "g++", []string{"-O2", "-pipe", "-static", "-s"})

	case model.LanguageJava:
		return compileJava(ctx, workDir, req.SourceCode)

	default:
		return "", "", "", false, &compileFailureError{log: fmt.Sprintf("unsupported language: %q", req.Language.String())}
	}
}

func compileNative(
	ctx context.Context,
	sourceCode string,
	sourcePath string,
	artifactPath string,
	compiler string,
	compileFlags []string,
) (string, string, string, bool, error) {
	if err := os.WriteFile(sourcePath, []byte(sourceCode), 0o644); err != nil {
		return "", "", "", false, fmt.Errorf("write source file: %w", err)
	}

	if !toolAvailable(compiler) {
		return sourcePath, "", "", false, &compileFailureError{log: compiler + " not found in PATH"}
	}

	args := append([]string{}, compileFlags...)
	args = append(args, "-o", artifactPath, sourcePath)

	cmd := exec.CommandContext(ctx, compiler, args...)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output

	if runErr := cmd.Run(); runErr != nil {
		return sourcePath, "", "", false, &compileFailureError{log: output.String()}
	}

	return sourcePath, artifactPath, output.String(), true, nil
}

func compileJava(ctx context.Context, workDir string, sourceCode string) (string, string, string, bool, error) {
	sourcePath := filepath.Join(workDir, "Main.java")
	classDir := filepath.Join(workDir, "classes")
	artifactPath := filepath.Join(workDir, "solution.jar")

	if err := os.WriteFile(sourcePath, []byte(sourceCode), 0o644); err != nil {
		return "", "", "", false, fmt.Errorf("write java source: %w", err)
	}

	if err := os.MkdirAll(classDir, 0o755); err != nil {
		return "", "", "", false, fmt.Errorf("create class dir: %w", err)
	}

	if !toolAvailable("javac") {
		return sourcePath, "", "", false, &compileFailureError{log: "javac not found in PATH"}
	}
	if !toolAvailable("jar") {
		return sourcePath, "", "", false, &compileFailureError{log: "jar not found in PATH"}
	}

	var output bytes.Buffer

	javacCmd := exec.CommandContext(ctx, "javac", "-encoding", "UTF-8", "-d", classDir, sourcePath)
	javacCmd.Stdout = &output
	javacCmd.Stderr = &output
	if runErr := javacCmd.Run(); runErr != nil {
		return sourcePath, "", "", false, &compileFailureError{log: output.String()}
	}

	jarCmd := exec.CommandContext(ctx, "jar", "--create", "--file", artifactPath, "--main-class", "Main", "-C", classDir, ".")
	jarCmd.Stdout = &output
	jarCmd.Stderr = &output
	if runErr := jarCmd.Run(); runErr != nil {
		return sourcePath, "", "", false, &compileFailureError{log: output.String()}
	}

	return sourcePath, artifactPath, output.String(), true, nil
}

func toolAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
