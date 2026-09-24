package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"sync"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"golang.org/x/sync/semaphore"
)

const maxTestCases = 64

// JudgeEngine handles full judge orchestration.
type JudgeEngine struct {
	language       language
	checker        checker
	externalFS     fs.FS
	concurrencySem *semaphore.Weighted
}

type judgePlan struct {
	sourceCode               string
	timeLimit                uint32
	memoryLimit              uint32
	cases                    []caseData
	compiler                 languageCompiler
	checker                  checkerSource
	checkerProvidedByRequest bool
}

type caseData struct {
	input          string
	expectedOutput string
}

// NewJudgeEngine creates a judge engine.
func NewJudgeEngine(
	executor execution.Executor,
	bundledFS fs.FS,
	externalFS fs.FS,
	maxConcurrent int,
) (*JudgeEngine, error) {
	if maxConcurrent <= 0 {
		return nil, fmt.Errorf("max concurrent judges must be positive, got %d", maxConcurrent)
	}

	checkerModule, err := newChecker(executor, bundledFS, externalFS)
	if err != nil {
		return nil, fmt.Errorf("initialize checker: %w", err)
	}

	return newJudgeEngine(newLanguage(executor), checkerModule, externalFS, maxConcurrent), nil
}

func newJudgeEngine(
	languageModule language,
	checkerModule checker,
	externalFS fs.FS,
	maxConcurrent int,
) *JudgeEngine {
	return &JudgeEngine{
		language:       languageModule,
		checker:        checkerModule,
		externalFS:     externalFS,
		concurrencySem: semaphore.NewWeighted(int64(maxConcurrent)),
	}
}

// Close cancels and waits for shared checker compilations. Call it after all
// Judge calls have returned.
func (s *JudgeEngine) Close() {
	s.checker.Close()
}

func validateJudgeRequest(req model.JudgeRequest) error {
	if strings.TrimSpace(req.SourceCode) == "" {
		return errors.New("sourceCode is required")
	}
	if req.Language == model.LanguageUnknown {
		return errors.New("language is required")
	}
	if req.TimeLimit == 0 {
		return errors.New("timeLimit must be positive")
	}
	if req.MemoryLimit == 0 {
		return errors.New("memoryLimit must be positive")
	}
	if len(req.TestCases) == 0 {
		return errors.New("testcases must not be empty")
	}
	if len(req.TestCases) > maxTestCases {
		return fmt.Errorf("testcases must contain at most %d cases", maxTestCases)
	}
	for index, testCase := range req.TestCases {
		if err := validateJudgeTestCase(index, testCase); err != nil {
			return err
		}
	}
	return nil
}

func validateJudgeTestCase(index int, testCase model.JudgeTestCase) error {
	hasInputFile := testCase.InputFile != ""
	hasExpectedOutputFile := testCase.ExpectedOutputFile != ""
	hasText := testCase.InputText != "" || testCase.ExpectedOutput != ""

	if hasText && (hasInputFile || hasExpectedOutputFile) {
		return fmt.Errorf("testcases[%d]: cannot mix text and file data", index)
	}
	if hasInputFile != hasExpectedOutputFile {
		return fmt.Errorf("testcases[%d]: inputFile and expectedOutputFile must be provided together", index)
	}
	if hasInputFile && !fs.ValidPath(testCase.InputFile) {
		return fmt.Errorf("testcases[%d]: inputFile must be a valid relative path", index)
	}
	if hasExpectedOutputFile && !fs.ValidPath(testCase.ExpectedOutputFile) {
		return fmt.Errorf("testcases[%d]: expectedOutputFile must be a valid relative path", index)
	}
	return nil
}

// Judge validates a request, reserves capacity, and materializes its resources
// before compiling and evaluating all test cases. An error means the request or
// its resources were rejected before compilation; later failures are JudgeResults.
func (s *JudgeEngine) Judge(ctx context.Context, req model.JudgeRequest) (model.JudgeResult, error) {
	if err := validateJudgeRequest(req); err != nil {
		return model.JudgeResult{}, err
	}

	compiler, err := s.language.Resolve(req.Language)
	if err != nil {
		return model.JudgeResult{}, err
	}
	choice, err := resolveChecker(req.Checker, req.CheckerSourceCode)
	if err != nil {
		return model.JudgeResult{}, err
	}

	if err := s.concurrencySem.Acquire(ctx, 1); err != nil {
		return failedBeforeRun("judge request cancelled or timed out while waiting for capacity"), nil
	}
	defer s.concurrencySem.Release(1)

	plan, err := s.materialize(req, compiler, choice)
	if err != nil {
		return model.JudgeResult{}, err
	}
	return executeJudgePlan(ctx, plan), nil
}

func (s *JudgeEngine) materialize(
	req model.JudgeRequest,
	compiler languageCompiler,
	choice checkerChoice,
) (judgePlan, error) {
	src, err := s.checker.Source(choice)
	if err != nil {
		return judgePlan{}, err
	}

	cases := make([]caseData, len(req.TestCases))
	for i, testCase := range req.TestCases {
		data, err := s.materializeCase(testCase)
		if err != nil {
			return judgePlan{}, fmt.Errorf("testcases[%d]: %w", i, err)
		}
		cases[i] = data
	}

	return judgePlan{
		sourceCode:               req.SourceCode,
		timeLimit:                req.TimeLimit,
		memoryLimit:              req.MemoryLimit,
		cases:                    cases,
		compiler:                 compiler,
		checker:                  src,
		checkerProvidedByRequest: choice.providedByRequest(),
	}, nil
}

func (s *JudgeEngine) materializeCase(testCase model.JudgeTestCase) (caseData, error) {
	if testCase.InputFile == "" {
		return caseData{input: testCase.InputText, expectedOutput: testCase.ExpectedOutput}, nil
	}
	if s.externalFS == nil {
		return caseData{}, fmt.Errorf("inputFile %q requires external resources", testCase.InputFile)
	}

	input, err := fs.ReadFile(s.externalFS, testCase.InputFile)
	if err != nil {
		return caseData{}, fmt.Errorf("inputFile %q is not available: %w", testCase.InputFile, err)
	}
	expectedOutput, err := fs.ReadFile(s.externalFS, testCase.ExpectedOutputFile)
	if err != nil {
		return caseData{}, fmt.Errorf(
			"expectedOutputFile %q is not available: %w",
			testCase.ExpectedOutputFile,
			err,
		)
	}

	return caseData{input: string(input), expectedOutput: string(expectedOutput)}, nil
}

func executeJudgePlan(ctx context.Context, plan judgePlan) model.JudgeResult {
	program, compileResult, err := plan.compiler.Compile(ctx, plan.sourceCode)
	if err != nil {
		slog.ErrorContext(ctx, "compile step failed", "error", err)
		return failedBeforeRun(fmt.Sprintf("compile infrastructure error: %v", err))
	}

	if !compileResult.Succeeded {
		return model.JudgeResult{
			Status:  model.JudgeStatusCompileError,
			Compile: compileResult,
			Cases:   []model.JudgeCaseResult{},
		}
	}

	compilation, err := plan.checker.Compile(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "checker setup failed", "error", err)
		return model.JudgeResult{
			Status:  model.JudgeStatusSystemError,
			Compile: compileResult,
			Cases:   []model.JudgeCaseResult{},
		}
	}
	if !compilation.compile.Succeeded {
		status := model.JudgeStatusSystemError
		if plan.checkerProvidedByRequest {
			status = model.JudgeStatusCheckerCompileError
		} else {
			slog.ErrorContext(ctx, "system checker compilation failed", "log", compilation.compile.Log)
		}
		return model.JudgeResult{
			Status:         status,
			Compile:        compileResult,
			CheckerCompile: &compilation.compile,
			Cases:          []model.JudgeCaseResult{},
		}
	}

	runner := caseRunner{
		program:     program,
		checker:     compilation.checker,
		userChecker: plan.checkerProvidedByRequest,
		timeLimit:   plan.timeLimit,
		memLimit:    plan.memoryLimit,
	}
	caseResults := runAllCases(ctx, runner, plan.cases)

	return model.JudgeResult{
		Status:         aggregateStatus(caseResults),
		Compile:        compileResult,
		CheckerCompile: &compilation.compile,
		Cases:          caseResults,
	}
}

// caseRunner holds the shared state for running all test cases in one judge session.
type caseRunner struct {
	program     compiledProgram
	checker     preparedChecker
	userChecker bool // checker was supplied by the request, not bundled
	timeLimit   uint32
	memLimit    uint32
}

// runAllCases executes all test cases concurrently.
// Actual parallelism is bounded by the execution module.
func runAllCases(ctx context.Context, r caseRunner, cases []caseData) []model.JudgeCaseResult {
	results := make([]model.JudgeCaseResult, len(cases))
	var wg sync.WaitGroup
	for i, tc := range cases {
		wg.Go(func() {
			results[i] = r.runCase(ctx, tc, i)
		})
	}
	wg.Wait()
	return results
}

func (r caseRunner) runCase(ctx context.Context, tc caseData, index int) model.JudgeCaseResult {
	runResult, err := r.program.Run(ctx, tc.input, r.timeLimit, r.memLimit)
	if err != nil {
		slog.ErrorContext(ctx, "program execution failed", "index", index, "error", err)
		return model.JudgeCaseResult{
			Verdict:   model.VerdictUKE,
			ExtraInfo: fmt.Sprintf("infrastructure error: %v", err),
		}
	}

	if runResult.Verdict != execution.VerdictOK {
		return judgeCaseResultFromExecution(runResult, convertVerdict(runResult.Verdict), runResult.ExtraInfo)
	}

	checkResult, err := r.checker.Check(ctx, tc.input, runResult.Stdout, tc.expectedOutput)
	if err != nil {
		slog.ErrorContext(ctx, "checker execution failed", "index", index, "error", err)
		return judgeCaseResultFromExecution(
			runResult,
			model.VerdictUKE,
			fmt.Sprintf("checker infrastructure error: %v", err),
		)
	}

	message := checkResult.Message
	if message == "" {
		switch checkResult.Outcome {
		case checkerRejected:
			message = "checker reported wrong answer"
		case checkerFailed:
			message = "checker execution failed"
		}
	}

	verdict := model.VerdictOK
	switch checkResult.Outcome {
	case checkerAccepted:
		verdict = model.VerdictOK
	case checkerRejected:
		verdict = model.VerdictWA
	case checkerFailed:
		verdict = model.VerdictUKE
		if r.userChecker {
			verdict = model.VerdictCheckerExecutionError
		} else {
			slog.ErrorContext(ctx, "system checker execution failed", "index", index, "details", message)
		}
	}

	return judgeCaseResultFromExecution(runResult, verdict, message)
}

func convertVerdict(v execution.Verdict) model.Verdict {
	switch v {
	case execution.VerdictOK:
		return model.VerdictOK
	case execution.VerdictTLE:
		return model.VerdictTLE
	case execution.VerdictMLE:
		return model.VerdictMLE
	case execution.VerdictOLE:
		return model.VerdictOLE
	case execution.VerdictRE:
		return model.VerdictRE
	default:
		return model.VerdictUKE
	}
}

func failedBeforeRun(log string) model.JudgeResult {
	return model.JudgeResult{
		Status:  model.JudgeStatusSystemError,
		Compile: model.CompileResult{Succeeded: false, Log: log},
		Cases:   []model.JudgeCaseResult{},
	}
}

func judgeCaseResultFromExecution(
	runResult execution.RunResult,
	verdict model.Verdict,
	extraInfo string,
) model.JudgeCaseResult {
	return model.JudgeCaseResult{
		Verdict:    verdict,
		Stdout:     runResult.Stdout,
		TimeUsed:   runResult.CPUTimeMs,
		MemoryUsed: runResult.MemoryMB,
		ExitCode:   runResult.ExitCode,
		ExtraInfo:  extraInfo,
	}
}

// aggregateStatus returns the overall judge-pipeline status for completed cases.
// System failures take priority over request-provided checker failures. User
// program outcomes such as WA and TLE do not change the overall status.
func aggregateStatus(cases []model.JudgeCaseResult) model.JudgeStatus {
	status := model.JudgeStatusOK
	for _, c := range cases {
		if c.Verdict == model.VerdictUKE {
			return model.JudgeStatusSystemError
		}
		if c.Verdict == model.VerdictCheckerExecutionError {
			status = model.JudgeStatusCheckerExecutionError
		}
	}
	return status
}
