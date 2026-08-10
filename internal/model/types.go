// Package model defines core domain types for the sandbox system.
package model

// Language identifies a programming language.
type Language string

// Language identifiers.
const (
	LanguageUnknown Language = ""
	LanguageC       Language = "C"
	LanguageCPP     Language = "C++"
	LanguageJava    Language = "Java"
	LanguagePython  Language = "Python"
)

// Verdict represents the execution result status.
type Verdict string

// Execution verdicts.
const (
	VerdictOK  Verdict = "OK"
	VerdictTLE Verdict = "TimeLimitExceeded"
	VerdictMLE Verdict = "MemoryLimitExceeded"
	VerdictOLE Verdict = "OutputLimitExceeded"
	VerdictRE  Verdict = "RuntimeError"
	VerdictWA  Verdict = "WrongAnswer"
	VerdictUKE Verdict = "UnknownError"

	VerdictCheckerExecutionError Verdict = "CheckerExecutionError"
)

// JudgeTestCase represents a single test case for judging.
type JudgeTestCase struct {
	InputText          string `json:"inputText"`
	ExpectedOutput     string `json:"expectedOutputText"`
	InputFile          string `json:"inputFile,omitempty"`
	ExpectedOutputFile string `json:"expectedOutputFile,omitempty"`
}

// JudgeRequest contains parameters for a full judge session.
type JudgeRequest struct {
	SourceCode        string          `json:"sourceCode"`
	Checker           string          `json:"checker,omitempty"`
	CheckerSourceCode string          `json:"checkerSourceCode,omitempty"`
	Language          Language        `json:"language"`
	TimeLimit         uint32          `json:"timeLimit"`   // CPU milliseconds, per test case
	MemoryLimit       uint32          `json:"memoryLimit"` // megabytes, per test case
	TestCases         []JudgeTestCase `json:"testcases"`
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

// JudgeStatus represents the overall pipeline outcome of a judge session.
// Unlike Verdict (which describes per-case results), JudgeStatus describes
// whether compilation and all required checks completed successfully.
type JudgeStatus string

// Judge status constants.
const (
	JudgeStatusOK           JudgeStatus = "OK" // all cases evaluated; check per-case verdicts for details
	JudgeStatusCompileError JudgeStatus = "CompileError"
	JudgeStatusSystemError  JudgeStatus = "SystemError"

	JudgeStatusCheckerCompileError   JudgeStatus = "CheckerCompileError"
	JudgeStatusCheckerExecutionError JudgeStatus = "CheckerExecutionError"
)

// JudgeResult contains the final judge outcome.
// Cases preserves the order of JudgeRequest.TestCases.
type JudgeResult struct {
	Status         JudgeStatus       `json:"status"`
	Compile        CompileResult     `json:"compile"`
	CheckerCompile *CompileResult    `json:"checkerCompile,omitempty"`
	Cases          []JudgeCaseResult `json:"cases"`
}
