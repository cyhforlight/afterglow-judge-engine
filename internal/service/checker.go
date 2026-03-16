package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"afterglow-judge-engine/internal/resource"
)

const (
	defaultCheckerName = "default"
	externalPrefix     = "external:"

	testlibHeaderKey = "testlib.h"

	// Checker execution file names
	checkerSourceFileName = "checker.cpp"
	checkerInputFileName  = "input.txt"
	checkerOutputFileName = "output.txt"
	checkerAnswerFileName = "answer.txt"

	// Checker execution limits
	checkerCPUTimeLimitMs = 3000
	checkerMemoryLimitMB  = 256
)

// ResourceStore provides read-only access to internal checker resources.
type ResourceStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Stat(ctx context.Context, key string) error
}

// CheckerLocation describes where a checker is stored.
type CheckerLocation struct {
	IsExternal bool   // true if "external:" prefix, false if builtin
	Path       string // For external: normalized path; for builtin: short name
}

// ResolveChecker converts a request checker name into a validated CheckerLocation.
func ResolveChecker(raw string) (CheckerLocation, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return CheckerLocation{Path: defaultCheckerName}, nil
	}

	if checkerPath, ok := strings.CutPrefix(name, externalPrefix); ok {
		normalizedPath, err := validateExternalCheckerPath(checkerPath)
		if err != nil {
			return CheckerLocation{}, err
		}
		return CheckerLocation{IsExternal: true, Path: normalizedPath}, nil
	}

	if err := validateCheckerShortName(name); err != nil {
		return CheckerLocation{}, err
	}
	return CheckerLocation{Path: name}, nil
}

// builtinCheckerPath constructs the resource key for a builtin checker.
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
	// Allow letters (any case), digits, underscore, and hyphen
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return fmt.Errorf("checker %q contains invalid characters (only letters, digits, _, - allowed)", name)
		}
	}
	return nil
}

func validateExternalCheckerPath(checkerPath string) (string, error) {
	normalizedPath, err := resource.NormalizeKey(checkerPath)
	if err != nil {
		return "", fmt.Errorf("invalid external checker path: %w", err)
	}
	if !strings.HasSuffix(normalizedPath, ".cpp") {
		return "", fmt.Errorf("external checker must be a .cpp file: %q", checkerPath)
	}
	return normalizedPath, nil
}
