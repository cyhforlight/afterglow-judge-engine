package model

import (
	"errors"
	"fmt"
	"strings"
)

const (
	defaultMaxTimeLimitMs = 10_000
	defaultMaxMemoryMB    = 1024
	defaultMaxTestCases   = 64
	defaultMaxSourceBytes = 256 * 1024
)

// JudgeLimits defines request-level policy limits for one synchronous judge request.
type JudgeLimits struct {
	MaxTimeLimitMs int
	MaxMemoryMB    int
	MaxTestCases   int
	MaxSourceBytes int
}

// DefaultJudgeLimits returns conservative defaults for a single-node synchronous judge engine.
func DefaultJudgeLimits() JudgeLimits {
	return JudgeLimits{
		MaxTimeLimitMs: defaultMaxTimeLimitMs,
		MaxMemoryMB:    defaultMaxMemoryMB,
		MaxTestCases:   defaultMaxTestCases,
		MaxSourceBytes: defaultMaxSourceBytes,
	}
}

// ValidateConfig checks that the configured policy limits are usable.
func (limits JudgeLimits) ValidateConfig() error {
	switch {
	case limits.MaxTimeLimitMs <= 0:
		return errors.New("MAX_TIME_LIMIT_MS must be positive")
	case limits.MaxMemoryMB <= 0:
		return errors.New("MAX_MEMORY_MB must be positive")
	case limits.MaxTestCases <= 0:
		return errors.New("MAX_TEST_CASES must be positive")
	case limits.MaxSourceBytes <= 0:
		return errors.New("MAX_SOURCE_SIZE_KB must be positive")
	default:
		return nil
	}
}

// ValidateJudgeRequest checks request limits that should hold for all entry points.
func ValidateJudgeRequest(req JudgeRequest, limits JudgeLimits) error {
	if err := limits.ValidateConfig(); err != nil {
		return err
	}
	if strings.TrimSpace(req.SourceCode) == "" {
		return errors.New("sourceCode is required")
	}
	if req.Language == LanguageUnknown {
		return errors.New("language is required")
	}
	if !req.Language.isSupported() {
		return fmt.Errorf("unsupported language %q; expected one of C, C++, Java, Python", req.Language)
	}
	if len(req.SourceCode) > limits.MaxSourceBytes {
		return fmt.Errorf("sourceCode must be at most %d bytes", limits.MaxSourceBytes)
	}
	if req.TimeLimit <= 0 {
		return errors.New("timeLimit must be positive")
	}
	if req.TimeLimit > limits.MaxTimeLimitMs {
		return fmt.Errorf("timeLimit must be at most %d ms", limits.MaxTimeLimitMs)
	}
	if req.MemoryLimit <= 0 {
		return errors.New("memoryLimit must be positive")
	}
	if req.MemoryLimit > limits.MaxMemoryMB {
		return fmt.Errorf("memoryLimit must be at most %d MB", limits.MaxMemoryMB)
	}
	if len(req.TestCases) == 0 {
		return errors.New("testcases must not be empty")
	}
	if len(req.TestCases) > limits.MaxTestCases {
		return fmt.Errorf("testcases must contain at most %d cases", limits.MaxTestCases)
	}
	for index, testCase := range req.TestCases {
		if err := validateJudgeTestCase(index, testCase); err != nil {
			return err
		}
	}
	return nil
}

func validateJudgeTestCase(index int, testCase JudgeTestCase) error {
	hasInputFile := strings.TrimSpace(testCase.InputFile) != ""
	hasOutputFile := strings.TrimSpace(testCase.ExpectedOutputFile) != ""

	if hasInputFile && testCase.InputText != "" {
		return fmt.Errorf("testcases[%d]: cannot provide both inputText and inputFile", index)
	}
	if hasOutputFile && testCase.ExpectedOutput != "" {
		return fmt.Errorf("testcases[%d]: cannot provide both expectedOutputText and expectedOutputFile", index)
	}
	return nil
}
