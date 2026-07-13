package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	defaultCheckerName = "default"
	externalPrefix     = "external:"

	testlibHeaderKey = "testlib.h"

	// Checker execution file names.
	checkerInputFileName  = "input.txt"
	checkerOutputFileName = "output.txt"
	checkerAnswerFileName = "answer.txt"

	// Checker execution limits.
	checkerCPUTimeLimitMs = 3000
	checkerMemoryLimitMB  = 256
)

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
