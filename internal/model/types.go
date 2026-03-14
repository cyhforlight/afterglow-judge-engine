// Package model defines core domain types for the sandbox system.
package model

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Language represents a supported programming language.
type Language int

// Supported programming languages.
const (
	LanguageUnknown Language = iota
	LanguageC
	LanguageCPP
	LanguageJava
	LanguagePython
)

// ParseLanguage converts a string to a Language constant.
func ParseLanguage(raw string) (Language, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "C":
		return LanguageC, nil
	case "C++", "CPP":
		return LanguageCPP, nil
	case "JAVA":
		return LanguageJava, nil
	case "PYTHON", "PY", "PY3":
		return LanguagePython, nil
	default:
		return LanguageUnknown, fmt.Errorf("unsupported language: %q", raw)
	}
}

func (l Language) String() string {
	switch l {
	case LanguageC:
		return "C"
	case LanguageCPP:
		return "C++"
	case LanguageJava:
		return "Java"
	case LanguagePython:
		return "Python"
	default:
		return "Unknown"
	}
}

// MarshalJSON implements json.Marshaler for Language.
func (l Language) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.String())
}

// Verdict represents the execution result status.
type Verdict int

// Execution verdicts.
const (
	VerdictUnknown Verdict = iota
	VerdictOK
	VerdictTLE
	VerdictMLE
	VerdictOLE
	VerdictRE
	VerdictWA
	VerdictCE
	VerdictUKE
)

func (v Verdict) String() string {
	switch v {
	case VerdictOK:
		return "OK"
	case VerdictTLE:
		return "TimeLimitExceeded"
	case VerdictMLE:
		return "MemoryLimitExceeded"
	case VerdictOLE:
		return "OutputLimitExceeded"
	case VerdictRE:
		return "RuntimeError"
	case VerdictWA:
		return "WrongAnswer"
	case VerdictCE:
		return "CompileError"
	case VerdictUKE:
		return "UnknownError"
	default:
		return "Unknown"
	}
}

// MarshalJSON implements json.Marshaler for Verdict.
func (v Verdict) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

// CompiledArtifact is a compiled program transferred by value between layers.
type CompiledArtifact struct {
	Data []byte
	Mode os.FileMode
}

// ExecuteResult contains the execution outcome and resource usage.
type ExecuteResult struct {
	Verdict    Verdict `json:"verdict"`
	Stdout     string  `json:"stdout"`
	TimeUsed   int     `json:"timeUsed"`   // milliseconds
	MemoryUsed int     `json:"memoryUsed"` // megabytes
	ExitCode   int     `json:"exitCode"`
	ExtraInfo  string  `json:"extraInfo"`
}

// JudgeTestCase represents a single test case for judging.
type JudgeTestCase struct {
	InputText          string `json:"inputText"`
	ExpectedOutput     string `json:"expectedOutputText"`
	InputFile          string `json:"inputFile,omitempty"`
	ExpectedOutputFile string `json:"expectedOutputFile,omitempty"`
}

// JudgeRequest contains parameters for a full judge session.
type JudgeRequest struct {
	SourceCode  string          `json:"sourceCode"`
	Checker     string          `json:"checker,omitempty"`
	Language    Language        `json:"language"`
	TimeLimit   int             `json:"timeLimit"`   // milliseconds, per test case
	MemoryLimit int             `json:"memoryLimit"` // megabytes, per test case
	TestCases   []JudgeTestCase `json:"testcases"`
}

// CompileResult contains compile phase details.
type CompileResult struct {
	Succeeded bool   `json:"succeeded"`
	Log       string `json:"log"`
}

// JudgeCaseResult contains one test case execution result.
type JudgeCaseResult struct {
	Verdict    Verdict `json:"verdict"`
	Stdout     string  `json:"stdout"`
	TimeUsed   int     `json:"timeUsed"`   // milliseconds
	MemoryUsed int     `json:"memoryUsed"` // megabytes
	ExitCode   int     `json:"exitCode"`
	ExtraInfo  string  `json:"extraInfo"`
}

// JudgeStatus represents the overall system-level outcome of a judge session.
// Unlike Verdict (which describes per-case test results like OK/WA/TLE),
// JudgeStatus describes whether the judge pipeline itself completed successfully.
type JudgeStatus int

const (
	JudgeStatusOK           JudgeStatus = iota + 1 // all cases evaluated; check per-case verdicts for details
	JudgeStatusCompileError                         // user code failed to compile
	JudgeStatusSystemError                          // infrastructure failure prevented evaluation
)

func (s JudgeStatus) String() string {
	switch s {
	case JudgeStatusOK:
		return "OK"
	case JudgeStatusCompileError:
		return "CompileError"
	case JudgeStatusSystemError:
		return "SystemError"
	default:
		return "Unknown"
	}
}

// MarshalJSON implements json.Marshaler for JudgeStatus.
func (s JudgeStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// JudgeResult contains the final judge outcome.
type JudgeResult struct {
	Status      JudgeStatus       `json:"status"`
	Compile     CompileResult     `json:"compile"`
	Cases       []JudgeCaseResult `json:"cases"`
	PassedCount int               `json:"passedCount"`
	TotalCount  int               `json:"totalCount"`
}
