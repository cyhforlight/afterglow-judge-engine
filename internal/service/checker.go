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
	checkerRunImageRef    = imageDebian12

	checkerCPUTimeLimitMs = 3000
	checkerMemoryLimitMB  = 256
)

// checker owns shared compilation state and provides source snapshots for compilation.
type checker interface {
	Source(choice checkerChoice) (checkerSource, error)
}

// checkerSource holds the source bytes for one checker and can compile them.
type checkerSource interface {
	Compile(ctx context.Context) (checkerCompilation, error)
}

// preparedChecker checks outputs using one compiled checker artifact.
// Check is safe for concurrent calls.
type preparedChecker interface {
	Check(ctx context.Context, input, actualOutput, expectedOutput string) (checkerResult, error)
}

type checkerOutcome uint8

const (
	checkerFailed checkerOutcome = iota
	checkerAccepted
	checkerRejected
)

type checkerCompilation struct {
	checker preparedChecker
	compile model.CompileResult
}

type checkerResult struct {
	Outcome checkerOutcome
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

type checkerKind uint8

const (
	checkerBuiltin checkerKind = iota
	checkerExternal
	checkerInline
)

type checkerChoice struct {
	kind  checkerKind
	value string
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

func (c *checkerEngine) Source(choice checkerChoice) (checkerSource, error) {
	source, err := c.readSource(choice)
	if err != nil {
		return nil, err
	}
	return &checkerSnapshot{compiler: c.compiler, source: source}, nil
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

func (p *checkerSnapshot) Compile(ctx context.Context) (checkerCompilation, error) {
	checker, compileResult, err := p.compiler.prepare(ctx, p.source)
	if err != nil {
		return checkerCompilation{}, err
	}
	return checkerCompilation{checker: checker, compile: compileResult}, nil
}

func (c *checkerEngine) readSource(choice checkerChoice) ([]byte, error) {
	if choice.kind == checkerInline {
		return []byte(choice.value), nil
	}

	if choice.kind == checkerExternal {
		if c.externalFS == nil {
			return nil, fmt.Errorf("external checker %q requires external resources", choice.value)
		}
		checkerSource, err := fs.ReadFile(c.externalFS, choice.value)
		if err != nil {
			return nil, fmt.Errorf("external checker %q is not available: %w", choice.value, err)
		}
		return checkerSource, nil
	}

	sourceKey := builtinCheckerPath(choice.value)
	checkerSource, err := fs.ReadFile(c.bundledFS, sourceKey)
	if err != nil {
		return nil, fmt.Errorf("builtin checker %q is not available: %w", choice.value, err)
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
		return checkerResult{}, err
	}

	message := cmp.Or(
		strings.TrimSpace(runOut.Stderr),
		strings.TrimSpace(runOut.Stdout),
		strings.TrimSpace(runOut.ExtraInfo),
	)

	result := checkerResult{Outcome: checkerFailed, Message: message}
	switch {
	case runOut.Verdict == model.VerdictOK && runOut.ExitCode == 0:
		result.Outcome = checkerAccepted
	case runOut.Verdict == model.VerdictRE && (runOut.ExitCode == 1 || runOut.ExitCode == 2):
		result.Outcome = checkerRejected
	}
	return result, nil
}

func (c checkerChoice) providedByRequest() bool {
	return c.kind == checkerInline
}

func resolveChecker(raw, inlineSource string) (checkerChoice, error) {
	name := strings.TrimSpace(raw)
	if inlineSource != "" {
		if strings.TrimSpace(inlineSource) == "" {
			return checkerChoice{}, errors.New("checkerSourceCode must not be blank")
		}
		if name != "" {
			return checkerChoice{}, errors.New("checker and checkerSourceCode cannot be provided together")
		}
		return checkerChoice{kind: checkerInline, value: inlineSource}, nil
	}

	if name == "" {
		return checkerChoice{kind: checkerBuiltin, value: defaultCheckerName}, nil
	}

	if checkerPath, ok := strings.CutPrefix(name, externalPrefix); ok {
		normalizedPath, err := validateExternalCheckerPath(checkerPath)
		if err != nil {
			return checkerChoice{}, err
		}
		return checkerChoice{kind: checkerExternal, value: normalizedPath}, nil
	}

	if err := validateCheckerShortName(name); err != nil {
		return checkerChoice{}, err
	}
	return checkerChoice{kind: checkerBuiltin, value: name}, nil
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
		ImageRef:     imageGCC12,
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
