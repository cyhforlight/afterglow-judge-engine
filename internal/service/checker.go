package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/workspace"
)

const (
	defaultCheckerName  = "default"
	externalPrefix      = "external:"
	checkerCacheEntries = 64

	testlibHeaderKey = "testlib.h"

	checkerInputFileName  = "input.txt"
	checkerOutputFileName = "output.txt"
	checkerAnswerFileName = "answer.txt"

	checkerCPUTimeLimitMs = 3000
	checkerMemoryLimitMB  = 256
)

// checker resolves a request reference without loading or compiling resources.
type checker interface {
	Resolve(reference string) (resolvedChecker, error)
}

// resolvedChecker represents a validated checker reference. Validate checks
// resource availability for request intake, while Prepare loads and compiles
// the checker independently of whether Validate was called.
type resolvedChecker interface {
	Validate() error
	Prepare(ctx context.Context) (preparedChecker, error)
}

// preparedChecker checks outputs using one compiled checker artifact.
// Check is safe for concurrent calls.
type preparedChecker interface {
	Check(ctx context.Context, input, actualOutput, expectedOutput string) (checkerResult, error)
}

type checkerResult struct {
	Verdict model.Verdict
	Message string
}

type checkerEngine struct {
	compiler   Compiler
	runner     Runner
	bundledFS  fs.FS
	externalFS fs.FS
}

type checkerReference struct {
	engine   *checkerEngine
	location checkerLocation
}

type compiledChecker struct {
	runner   Runner
	artifact model.CompiledArtifact
}

type checkerLocation struct {
	isExternal bool
	path       string
}

func newChecker(compiler Compiler, runner Runner, bundledFS, externalFS fs.FS) (checker, error) {
	cachedCompiler, err := NewCachedCompiler(compiler, checkerCacheEntries)
	if err != nil {
		return nil, fmt.Errorf("create checker compile cache: %w", err)
	}

	return &checkerEngine{
		compiler:   cachedCompiler,
		runner:     runner,
		bundledFS:  bundledFS,
		externalFS: externalFS,
	}, nil
}

func (c *checkerEngine) Resolve(reference string) (resolvedChecker, error) {
	location, err := resolveChecker(reference)
	if err != nil {
		return nil, err
	}
	return &checkerReference{engine: c, location: location}, nil
}

func (r *checkerReference) Validate() error {
	location := r.location
	if location.isExternal {
		if r.engine.externalFS == nil {
			return fmt.Errorf("external checker %q requires external resources", location.path)
		}
		if _, err := fs.Stat(r.engine.externalFS, location.path); err != nil {
			return fmt.Errorf("external checker %q is not available: %w", location.path, err)
		}
	} else {
		sourceKey := builtinCheckerPath(location.path)
		if _, err := fs.Stat(r.engine.bundledFS, sourceKey); err != nil {
			return fmt.Errorf("builtin checker %q is not available: %w", location.path, err)
		}
	}

	if _, err := fs.Stat(r.engine.bundledFS, testlibHeaderKey); err != nil {
		return fmt.Errorf("checker dependency %q is not available: %w", testlibHeaderKey, err)
	}
	return nil
}

func (r *checkerReference) Prepare(ctx context.Context) (preparedChecker, error) {
	checkerSource, err := r.loadSource()
	if err != nil {
		return nil, fmt.Errorf("checker setup failed: %w", err)
	}

	testlibHeader, err := fs.ReadFile(r.engine.bundledFS, testlibHeaderKey)
	if err != nil {
		return nil, fmt.Errorf("checker setup failed: load %q: %w", testlibHeaderKey, err)
	}

	profile := checkerProfile()
	compileOut, err := r.engine.compiler.Compile(ctx, CompileRequest{
		Files: []workspace.File{
			{Name: profile.Compile.SourceFile, Content: checkerSource, Mode: 0o644},
			{Name: testlibHeaderKey, Content: testlibHeader, Mode: 0o644},
		},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand,
		ArtifactName: profile.Compile.ArtifactName,
		Limits: execution.Limits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * execution.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: execution.DefaultCompileOutputLimitBytes,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("checker setup failed: %w", err)
	}
	if !compileOut.Result.Succeeded {
		message := strings.TrimSpace(compileOut.Result.Log)
		if message == "" {
			message = "checker compilation failed"
		}
		return nil, fmt.Errorf("checker compilation failed: %s", message)
	}
	if compileOut.Artifact == nil {
		return nil, errors.New("checker compilation succeeded without artifact")
	}

	return &compiledChecker{runner: r.engine.runner, artifact: *compileOut.Artifact}, nil
}

func (r *checkerReference) loadSource() ([]byte, error) {
	location := r.location
	if location.isExternal {
		if r.engine.externalFS == nil {
			return nil, errors.New("external resources not configured")
		}
		checkerSource, err := fs.ReadFile(r.engine.externalFS, location.path)
		if err != nil {
			return nil, fmt.Errorf("load external checker %q: %w", location.path, err)
		}
		return checkerSource, nil
	}

	sourceKey := builtinCheckerPath(location.path)
	checkerSource, err := fs.ReadFile(r.engine.bundledFS, sourceKey)
	if err != nil {
		return nil, fmt.Errorf("load builtin checker %q from %q: %w", location.path, sourceKey, err)
	}
	return checkerSource, nil
}

func (c *compiledChecker) Check(
	ctx context.Context,
	input string,
	actualOutput string,
	expectedOutput string,
) (checkerResult, error) {
	if len(c.artifact.Data) == 0 {
		return checkerResult{Verdict: model.VerdictUKE}, errors.New("checker artifact is required")
	}

	profile := checkerProfile()
	runOut, err := c.runner.Run(ctx, RunRequest{
		Files: []workspace.File{
			{Name: profile.Run.ArtifactName, Content: c.artifact.Data, Mode: c.artifact.Mode},
			{Name: checkerInputFileName, Content: []byte(input), Mode: 0o644},
			{Name: checkerOutputFileName, Content: []byte(actualOutput), Mode: 0o644},
			{Name: checkerAnswerFileName, Content: []byte(expectedOutput), Mode: 0o644},
		},
		ImageRef: profile.Run.ImageRef,
		Command: []string{
			runMountDir + "/" + profile.Run.ArtifactName,
			runMountDir + "/" + checkerInputFileName,
			runMountDir + "/" + checkerOutputFileName,
			runMountDir + "/" + checkerAnswerFileName,
		},
		Limits: checkerRunLimits(),
	})
	if err != nil {
		return checkerResult{Verdict: model.VerdictUKE}, err
	}

	message := strings.TrimSpace(runOut.Stderr)
	if message == "" {
		message = strings.TrimSpace(runOut.Stdout)
	}
	if message == "" {
		message = strings.TrimSpace(runOut.ExtraInfo)
	}

	result := checkerResult{Verdict: model.VerdictUKE, Message: message}
	switch runOut.Verdict {
	case execution.VerdictTLE, execution.VerdictMLE, execution.VerdictOLE:
		return result, nil
	}

	switch runOut.ExitCode {
	case 0:
		if runOut.Verdict == execution.VerdictOK {
			result.Verdict = model.VerdictOK
		}
	case 1, 2:
		result.Verdict = model.VerdictWA
	}
	return result, nil
}

func resolveChecker(raw string) (checkerLocation, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return checkerLocation{path: defaultCheckerName}, nil
	}

	if checkerPath, ok := strings.CutPrefix(name, externalPrefix); ok {
		normalizedPath, err := validateExternalCheckerPath(checkerPath)
		if err != nil {
			return checkerLocation{}, err
		}
		return checkerLocation{isExternal: true, path: normalizedPath}, nil
	}

	if err := validateCheckerShortName(name); err != nil {
		return checkerLocation{}, err
	}
	return checkerLocation{path: name}, nil
}

func builtinCheckerPath(shortName string) string {
	return fmt.Sprintf("checkers/%s.cpp", shortName)
}

func validateCheckerShortName(name string) error {
	if name == "" {
		return errors.New("checker name must not be empty")
	}
	if strings.ContainsAny(name, `/\.`) {
		return fmt.Errorf("checker %q contains invalid path characters (/, \\, .)", name)
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return fmt.Errorf("checker %q contains invalid characters (only letters, digits, _, - allowed)", name)
		}
	}
	return nil
}

func validateExternalCheckerPath(checkerPath string) (string, error) {
	if strings.TrimSpace(checkerPath) == "" {
		return "", errors.New("external checker path is required")
	}

	normalizedPath := filepath.Clean(checkerPath)
	if normalizedPath == "." {
		return "", errors.New("external checker path is required")
	}
	if !filepath.IsLocal(normalizedPath) {
		return "", fmt.Errorf("external checker path escapes resource root: %q", checkerPath)
	}
	if !strings.HasSuffix(normalizedPath, ".cpp") {
		return "", fmt.Errorf("external checker must be a .cpp file: %q", checkerPath)
	}
	return normalizedPath, nil
}

func checkerProfile() LanguageProfile {
	return LanguageProfile{
		Compile: CompileConfig{
			ImageRef:     gccImage,
			SourceFile:   "checker.cpp",
			ArtifactName: "checker",
			BuildCommand: []string{
				"g++", "-std=c++20", optimizationFlag, pipeFlag, staticLinkFlag, "-s",
				"-o", compileMountDir + "/checker",
				compileMountDir + "/checker.cpp", mathLibraryFlag,
			},
			TimeoutMs: 30000,
			MemoryMB:  512,
		},
		Run: RunConfig{
			ImageRef:       staticRuntimeImage,
			ArtifactName:   "checker",
			RuntimeCommand: func(p string, _ int) []string { return []string{p} },
		},
	}
}

func checkerRunLimits() execution.Limits {
	return execution.Limits{
		CPUTimeMs:   checkerCPUTimeLimitMs,
		WallTimeMs:  checkerCPUTimeLimitMs * execution.WallTimeMultiplier,
		MemoryMB:    checkerMemoryLimitMB,
		OutputBytes: execution.DefaultRunOutputLimitBytes,
	}
}
