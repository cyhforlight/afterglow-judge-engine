// Package model defines core domain types for the sandbox system.
package model

import (
	"encoding/json"
)

const unknownString = "Unknown"

func stringOrUnknown(value string) string {
	if value == "" {
		return unknownString
	}
	return value
}

// Language represents a supported programming language.
type Language string

// Supported programming languages.
const (
	LanguageUnknown Language = ""
	LanguageC       Language = "C"
	LanguageCPP     Language = "C++"
	LanguageJava    Language = "Java"
	LanguagePython  Language = "Python"
)

// IsSupported reports whether the language can be judged.
func (l Language) IsSupported() bool {
	switch l {
	case LanguageC, LanguageCPP, LanguageJava, LanguagePython:
		return true
	default:
		return false
	}
}

func (l Language) String() string {
	return stringOrUnknown(string(l))
}

// MarshalJSON implements json.Marshaler for Language.
func (l Language) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.String())
}

// Verdict represents the execution result status.
type Verdict string

// Execution verdicts.
const (
	VerdictUnknown Verdict = ""
	VerdictOK      Verdict = "OK"
	VerdictTLE     Verdict = "TimeLimitExceeded"
	VerdictMLE     Verdict = "MemoryLimitExceeded"
	VerdictOLE     Verdict = "OutputLimitExceeded"
	VerdictRE      Verdict = "RuntimeError"
	VerdictWA      Verdict = "WrongAnswer"
	VerdictUKE     Verdict = "UnknownError"
)

func (v Verdict) String() string {
	return stringOrUnknown(string(v))
}

// MarshalJSON implements json.Marshaler for Verdict.
func (v Verdict) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
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
	TimeLimit   int             `json:"timeLimit"`   // CPU milliseconds, per test case
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
	TimeUsed   int     `json:"timeUsed"`   // CPU milliseconds
	MemoryUsed int     `json:"memoryUsed"` // megabytes
	ExitCode   int     `json:"exitCode"`
	ExtraInfo  string  `json:"extraInfo"`
}

// JudgeStatus represents the overall system-level outcome of a judge session.
// Unlike Verdict (which describes per-case test results like OK/WA/TLE),
// JudgeStatus describes whether the judge pipeline itself completed successfully.
type JudgeStatus string

// Judge status constants.
const (
	JudgeStatusOK           JudgeStatus = "OK" // all cases evaluated; check per-case verdicts for details
	JudgeStatusCompileError JudgeStatus = "CompileError"
	JudgeStatusSystemError  JudgeStatus = "SystemError"
)

func (s JudgeStatus) String() string {
	return stringOrUnknown(string(s))
}

// MarshalJSON implements json.Marshaler for JudgeStatus.
func (s JudgeStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// JudgeResult contains the final judge outcome.
// Cases preserves the order of JudgeRequest.TestCases.
type JudgeResult struct {
	Status  JudgeStatus       `json:"status"`
	Compile CompileResult     `json:"compile"`
	Cases   []JudgeCaseResult `json:"cases"`
}
