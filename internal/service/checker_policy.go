package service

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"afterglow-judge-sandbox/internal/storage"
)

const (
	defaultCheckerName = "default"
	externalPrefix     = "external:"
)

var builtinCheckers = []string{
	defaultCheckerName,
	"ncmp",
	"wcmp",
	"fcmp",
	"yesno",
	"nyesno",
	"lcmp",
	"hcmp",
	"rcmp4",
	"rcmp6",
	"rcmp9",
}

// CheckerLocation describes where a checker is stored.
type CheckerLocation struct {
	IsExternal bool   // true if "external:" prefix, false if builtin
	Path       string // For external: normalized path; for builtin: short name
}

// BuiltinCheckerNames returns the bundled checker short names.
func BuiltinCheckerNames() []string {
	return slices.Clone(builtinCheckers)
}

// CheckerPolicy resolves request checker names into bundled storage keys.
type CheckerPolicy struct {
	defaultChecker      string
	allowed             []string
	allowedSet          map[string]struct{} // exact matches
	allowExternalAll    bool                // true if "external:*" present
	allowExternalPrefix []string            // prefixes like "external:contest-2024/"
}

// NewCheckerPolicy creates a validated checker policy.
func NewCheckerPolicy(defaultChecker string, allowedCheckers []string) (*CheckerPolicy, error) {
	defaultChecker = strings.TrimSpace(defaultChecker)
	if defaultChecker == "" {
		defaultChecker = defaultCheckerName
	}

	if len(allowedCheckers) == 0 {
		allowedCheckers = BuiltinCheckerNames()
	}

	allowed := make([]string, 0, len(allowedCheckers))
	allowedSet := make(map[string]struct{}, len(allowedCheckers))
	allowExternalAll := false
	allowExternalPrefix := []string{}

	for _, raw := range allowedCheckers {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}

		// Handle wildcard patterns
		if name == "external:*" {
			allowExternalAll = true
			allowed = append(allowed, name)
			continue
		}
		if strings.HasPrefix(name, externalPrefix) && strings.HasSuffix(name, "/*") {
			prefix := strings.TrimSuffix(name, "*")
			allowExternalPrefix = append(allowExternalPrefix, prefix)
			allowed = append(allowed, name)
			continue
		}

		// Validate builtin checker names
		if !strings.HasPrefix(name, externalPrefix) {
			if err := validateCheckerShortName(name); err != nil {
				return nil, fmt.Errorf("allowed checker %q: %w", raw, err)
			}
		}

		if _, exists := allowedSet[name]; exists {
			continue
		}
		allowed = append(allowed, name)
		allowedSet[name] = struct{}{}
	}

	policy := &CheckerPolicy{
		defaultChecker:      defaultChecker,
		allowed:             allowed,
		allowedSet:          allowedSet,
		allowExternalAll:    allowExternalAll,
		allowExternalPrefix: allowExternalPrefix,
	}
	if err := policy.ValidateConfig(); err != nil {
		return nil, err
	}

	return policy, nil
}

// ValidateConfig verifies checker policy configuration at startup.
func (p *CheckerPolicy) ValidateConfig() error {
	if p == nil {
		return errors.New("checker policy is required")
	}
	if err := validateCheckerShortName(p.defaultChecker); err != nil {
		return fmt.Errorf("default checker %q: %w", p.defaultChecker, err)
	}
	if len(p.allowed) == 0 {
		return errors.New("allowed checkers must not be empty")
	}
	if _, ok := p.allowedSet[p.defaultChecker]; !ok {
		return fmt.Errorf("default checker %q is not in allowed checkers %v", p.defaultChecker, p.allowed)
	}

	return nil
}

// Resolve converts a request checker into a validated CheckerLocation.
func (p *CheckerPolicy) Resolve(raw string) (CheckerLocation, error) {
	if p == nil {
		return CheckerLocation{}, errors.New("checker policy is required")
	}

	name := strings.TrimSpace(raw)
	if name == "" {
		return CheckerLocation{IsExternal: false, Path: p.defaultChecker}, nil
	}

	// Handle external checkers
	if checkerPath, ok := strings.CutPrefix(name, externalPrefix); ok {
		normalizedPath, err := validateExternalCheckerPath(checkerPath)
		if err != nil {
			return CheckerLocation{}, err
		}

		// Check if external checkers are allowed
		if !p.isExternalCheckerAllowed(name) {
			return CheckerLocation{}, fmt.Errorf("checker %q is not allowed (allowed: %v)", name, p.allowed)
		}

		return CheckerLocation{IsExternal: true, Path: normalizedPath}, nil
	}

	// Handle builtin checkers
	if err := validateCheckerShortName(name); err != nil {
		return CheckerLocation{}, err
	}
	if _, ok := p.allowedSet[name]; !ok {
		return CheckerLocation{}, fmt.Errorf("checker %q is not allowed (allowed: %v)", name, p.allowed)
	}

	return CheckerLocation{IsExternal: false, Path: name}, nil
}

// isExternalCheckerAllowed checks if an external checker is allowed by policy.
func (p *CheckerPolicy) isExternalCheckerAllowed(fullName string) bool {
	// Check exact match
	if _, ok := p.allowedSet[fullName]; ok {
		return true
	}

	// Check wildcard
	if p.allowExternalAll {
		return true
	}

	// Check prefix patterns
	for _, prefix := range p.allowExternalPrefix {
		if strings.HasPrefix(fullName, prefix) {
			return true
		}
	}

	return false
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

// validateExternalCheckerPath validates and normalizes an external checker path.
func validateExternalCheckerPath(checkerPath string) (string, error) {
	// Reuse storage.normalizeResourceKey for path validation
	normalizedPath, err := storage.NormalizeResourceKey(checkerPath)
	if err != nil {
		return "", fmt.Errorf("invalid external checker path: %w", err)
	}

	// Additional check: must end with .cpp
	if !strings.HasSuffix(normalizedPath, ".cpp") {
		return "", fmt.Errorf("external checker must be a .cpp file: %q", checkerPath)
	}

	return normalizedPath, nil
}
