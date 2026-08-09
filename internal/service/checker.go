package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"
)

const (
	defaultCheckerName = "default"
	externalPrefix     = "external:"

	testlibHeaderKey = "testlib.h"

	checkerInputFileName  = "input.txt"
	checkerOutputFileName = "output.txt"
	checkerAnswerFileName = "answer.txt"
	checkerArtifactName   = "checker"
	checkerRunImageRef    = "docker.io/library/debian:12-slim"

	checkerCPUTimeLimitMs = 3000
	checkerMemoryLimitMB  = 256
)

// checker materializes a resolved location into an immutable compile plan.
type checker interface {
	Materialize(location checkerLocation) (checkerPlan, error)
}

// checkerPlan owns the source snapshot used to compile one checker.
type checkerPlan interface {
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
	compiler   *checkerCompiler
	bundledFS  fs.FS
	externalFS fs.FS
}

type checkerSnapshot struct {
	compiler *checkerCompiler
	source   []byte
}

type compiledChecker struct {
	executor execution.Executor
	artifact execution.Artifact
}

type checkerLocation struct {
	isExternal bool
	path       string
}

func newChecker(executor execution.Executor, bundledFS, externalFS fs.FS) (checker, error) {
	testlibHeader, err := fs.ReadFile(bundledFS, testlibHeaderKey)
	if err != nil {
		return nil, fmt.Errorf("checker dependency %q is not available: %w", testlibHeaderKey, err)
	}
	defaultCheckerPath := builtinCheckerPath(defaultCheckerName)
	if err := validateResourceFile(bundledFS, defaultCheckerPath); err != nil {
		return nil, fmt.Errorf("checker dependency %q is not available: %w", defaultCheckerPath, err)
	}

	compiler, err := newCheckerCompiler(executor, testlibHeader)
	if err != nil {
		return nil, fmt.Errorf("create checker compile cache: %w", err)
	}

	return &checkerEngine{
		compiler:   compiler,
		bundledFS:  bundledFS,
		externalFS: externalFS,
	}, nil
}

func (c *checkerEngine) Materialize(location checkerLocation) (checkerPlan, error) {
	source, err := c.readSource(location)
	if err != nil {
		return nil, err
	}

	return &checkerSnapshot{
		compiler: c.compiler,
		source:   source,
	}, nil
}

func validateResourceFile(fsys fs.FS, name string) error {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q is not a regular file", name)
	}
	return nil
}

func (p *checkerSnapshot) Prepare(ctx context.Context) (preparedChecker, error) {
	return p.compiler.prepare(ctx, p.source)
}

func (c *checkerEngine) readSource(location checkerLocation) ([]byte, error) {
	if location.isExternal {
		if c.externalFS == nil {
			return nil, fmt.Errorf("external checker %q requires external resources", location.path)
		}
		checkerSource, err := fs.ReadFile(c.externalFS, location.path)
		if err != nil {
			return nil, fmt.Errorf("external checker %q is not available: %w", location.path, err)
		}
		return checkerSource, nil
	}

	sourceKey := builtinCheckerPath(location.path)
	checkerSource, err := fs.ReadFile(c.bundledFS, sourceKey)
	if err != nil {
		return nil, fmt.Errorf("builtin checker %q is not available: %w", location.path, err)
	}
	return checkerSource, nil
}

func (c *compiledChecker) Check(
	ctx context.Context,
	input string,
	actualOutput string,
	expectedOutput string,
) (checkerResult, error) {
	runOut, err := c.executor.Run(ctx, execution.RunRequest{
		Artifact: c.artifact,
		Files: []execution.File{
			{Name: checkerInputFileName, Content: []byte(input), Mode: 0o644},
			{Name: checkerOutputFileName, Content: []byte(actualOutput), Mode: 0o644},
			{Name: checkerAnswerFileName, Content: []byte(expectedOutput), Mode: 0o644},
		},
		ImageRef: checkerRunImageRef,
		Command: []string{
			"./" + c.artifact.Name,
			checkerInputFileName,
			checkerOutputFileName,
			checkerAnswerFileName,
		},
		Limits: checkerRunLimits(),
	})
	if err != nil {
		return checkerResult{Verdict: model.VerdictUKE}, err
	}

	message := cmp.Or(
		strings.TrimSpace(runOut.Stderr),
		strings.TrimSpace(runOut.Stdout),
		strings.TrimSpace(runOut.ExtraInfo),
	)

	result := checkerResult{Verdict: model.VerdictUKE, Message: message}
	switch runOut.Verdict {
	case execution.VerdictTLE, execution.VerdictMLE, execution.VerdictOLE:
		return result, nil
	}

	switch runOut.ExitCode {
	case 0:
		result.Verdict = model.VerdictOK
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

func checkerCompileProfile() compileConfig {
	return compileConfig{
		ImageRef:     "docker.io/library/gcc:12-bookworm",
		SourceFile:   "checker.cpp",
		ArtifactName: checkerArtifactName,
		BuildCommand: []string{
			"g++", "-std=c++20", "-O2", "-pipe", "-static", "-s",
			"-o", checkerArtifactName, "checker.cpp", "-lm",
		},
		TimeoutMs: 30000,
		MemoryMB:  512,
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
