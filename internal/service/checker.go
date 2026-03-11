package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"afterglow-judge-engine/internal/storage"
)

const (
	defaultCheckerName = "default"
	externalPrefix     = "external:"

	testlibHeaderKey = "testlib.h"

	checkerSourceFileName   = "checker.cpp"
	checkerArtifactFileName = "checker"
	checkerInputFileName    = "input.txt"
	checkerOutputFileName   = "output.txt"
	checkerAnswerFileName   = "answer.txt"

	checkerCPUTimeLimitMs = 3000
	checkerMemoryLimitMB  = 256
)

// ResourceStore provides read-only access to internal checker resources.
type ResourceStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// CheckerLocation describes where a checker is stored.
type CheckerLocation struct {
	IsExternal bool   // true if "external:" prefix, false if builtin
	Path       string // For external: normalized path; for builtin: short name
}

// ResolveChecker converts a request checker name into a validated CheckerLocation.
func ResolveChecker(raw, defaultChecker string) (CheckerLocation, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return CheckerLocation{Path: defaultChecker}, nil
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

func validateCheckerShortName(name string) error {
	if name == "" {
		return errors.New("checker name must not be empty")
	}
	if strings.ContainsAny(name, `/\.`) {
		return fmt.Errorf("checker %q must be a builtin short name", name)
	}
	for _, r := range name {
		if !unicode.IsLower(r) && !unicode.IsDigit(r) {
			return fmt.Errorf("checker %q must be a builtin short name", name)
		}
	}
	return nil
}

func validateExternalCheckerPath(checkerPath string) (string, error) {
	normalizedPath, err := storage.NormalizeResourceKey(checkerPath)
	if err != nil {
		return "", fmt.Errorf("invalid external checker path: %w", err)
	}
	if !strings.HasSuffix(normalizedPath, ".cpp") {
		return "", fmt.Errorf("external checker must be a .cpp file: %q", checkerPath)
	}
	return normalizedPath, nil
}
