package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"golang.org/x/sync/semaphore"
)

// JudgeEngine handles full judge orchestration.
type JudgeEngine struct {
	language       language
	checker        checker
	externalFS     fs.FS
	concurrencySem *semaphore.Weighted
	limits         model.JudgeLimits
}

type admittedJudge struct {
	request  model.JudgeRequest
	compiler languageCompiler
	checker  resolvedChecker
}

// NewJudgeEngine creates a judge engine.
func NewJudgeEngine(
	executor execution.Executor,
	bundledFS fs.FS,
	externalFS fs.FS,
	maxConcurrent int,
	limits model.JudgeLimits,
) (*JudgeEngine, error) {
	if maxConcurrent <= 0 {
		return nil, fmt.Errorf("max concurrent judges must be positive, got %d", maxConcurrent)
	}
	if err := validateJudgeLimits(limits); err != nil {
		return nil, fmt.Errorf("invalid judge limits: %w", err)
	}

	compiler := newCompiler(executor)
	runner := newRunner(executor)
	checkerModule, err := newChecker(compiler, runner, bundledFS, externalFS)
	if err != nil {
		return nil, fmt.Errorf("initialize checker: %w", err)
	}

	return newJudgeEngine(newLanguage(compiler, runner), checkerModule, externalFS, maxConcurrent, limits), nil
}

func validateJudgeLimits(limits model.JudgeLimits) error {
	const bytesPerMiB = int64(1024 * 1024)

	switch {
	case limits.MaxTimeLimitMs <= 0:
		return errors.New("maximum time limit must be positive")
	case limits.MaxMemoryMB <= 0:
		return errors.New("maximum memory limit must be positive")
	case limits.MaxTestCases <= 0:
		return errors.New("maximum testcase count must be positive")
	case limits.MaxSourceBytes <= 0:
		return errors.New("maximum source size must be positive")
	case limits.MaxTimeLimitMs > math.MaxInt/execution.WallTimeMultiplier ||
		int64(limits.MaxTimeLimitMs) > math.MaxInt64/(int64(execution.WallTimeMultiplier)*int64(time.Millisecond)):
		return fmt.Errorf("maximum time limit is too large: %dms", limits.MaxTimeLimitMs)
	}

	javaReserveMB := max(javaNativeReserveMB, limits.MaxMemoryMB/4)
	if limits.MaxMemoryMB > math.MaxInt-javaReserveMB ||
		int64(limits.MaxMemoryMB+javaReserveMB) > math.MaxInt64/bytesPerMiB {
		return fmt.Errorf("maximum memory limit is too large: %dMB", limits.MaxMemoryMB)
	}

	return nil
}

func newJudgeEngine(
	languageModule language,
	checkerModule checker,
	externalFS fs.FS,
	maxConcurrent int,
	limits model.JudgeLimits,
) *JudgeEngine {
	return &JudgeEngine{
		language:       languageModule,
		checker:        checkerModule,
		externalFS:     externalFS,
		concurrencySem: semaphore.NewWeighted(int64(maxConcurrent)),
		limits:         limits,
	}
}

func (s *JudgeEngine) admit(req model.JudgeRequest) (admittedJudge, error) {
	if err := validateJudgeRequest(req, s.limits); err != nil {
		return admittedJudge{}, err
	}

	compiler, err := s.language.Resolve(req.Language)
	if err != nil {
		return admittedJudge{}, err
	}

	resolved, err := s.checker.Resolve(req.Checker)
	if err != nil {
		return admittedJudge{}, err
	}

	if err := resolved.Validate(); err != nil {
		return admittedJudge{}, err
	}

	for index, testCase := range req.TestCases {
		if testCase.InputFile == "" {
			continue
		}
		if err := s.validateExternalDependency(testCase.InputFile, "inputFile"); err != nil {
			return admittedJudge{}, fmt.Errorf("testcases[%d]: %w", index, err)
		}
		if err := s.validateExternalDependency(testCase.ExpectedOutputFile, "expectedOutputFile"); err != nil {
			return admittedJudge{}, fmt.Errorf("testcases[%d]: %w", index, err)
		}
	}

	return admittedJudge{request: req, compiler: compiler, checker: resolved}, nil
}

func validateJudgeRequest(req model.JudgeRequest, limits model.JudgeLimits) error {
	if strings.TrimSpace(req.SourceCode) == "" {
		return errors.New("sourceCode is required")
	}
	if req.Language == model.LanguageUnknown {
		return errors.New("language is required")
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
	return nil
}

func (s *JudgeEngine) validateExternalDependency(path, label string) error {
	if s.externalFS == nil {
		return fmt.Errorf("%s %q requires external resources", label, path)
	}
	if err := validateResourceFile(s.externalFS, path); err != nil {
		return fmt.Errorf("%s %q is not available: %w", label, path, err)
	}
	return nil
}

// Judge admits a request, then compiles its source code and evaluates all test cases.
// An error means the request was rejected before judging started. Once admitted,
// all failures are represented by the returned JudgeResult.
func (s *JudgeEngine) Judge(ctx context.Context, req model.JudgeRequest) (model.JudgeResult, error) {
	admitted, err := s.admit(req)
	if err != nil {
		return model.JudgeResult{}, err
	}
	return s.run(ctx, admitted), nil
}

func (s *JudgeEngine) run(ctx context.Context, admitted admittedJudge) model.JudgeResult {
	req := admitted.request

	if err := s.concurrencySem.Acquire(ctx, 1); err != nil {
		return failedBeforeRun("judge request cancelled or timed out while waiting for capacity")
	}
	defer s.concurrencySem.Release(1)

	program, compileResult, err := admitted.compiler.Compile(ctx, req.SourceCode)
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

	prepared, err := admitted.checker.Prepare(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "checker setup failed", "error", err)
		return model.JudgeResult{
			Status:  model.JudgeStatusSystemError,
			Compile: compileResult,
			Cases:   []model.JudgeCaseResult{},
		}
	}

	caseResults := s.runAllCases(ctx, req, program, prepared)

	return model.JudgeResult{
		Status:  aggregateStatus(caseResults),
		Compile: compileResult,
		Cases:   caseResults,
	}
}

// runAllCases loads test data and executes each case concurrently.
// Actual parallelism is bounded by the execution module.
func (s *JudgeEngine) runAllCases(
	ctx context.Context,
	req model.JudgeRequest,
	program compiledProgram,
	prepared preparedChecker,
) []model.JudgeCaseResult {
	results := make([]model.JudgeCaseResult, len(req.TestCases))

	var wg sync.WaitGroup
	for i, tc := range req.TestCases {
		wg.Go(func() {
			if err := s.loadTestCaseData(&tc); err != nil {
				slog.ErrorContext(ctx, "failed to load test case data", "index", i, "error", err)
				results[i] = model.JudgeCaseResult{
					Verdict:   model.VerdictUKE,
					ExtraInfo: fmt.Sprintf("test data loading failed: %v", err),
				}
				return
			}
			results[i] = runSingleCase(ctx, req, program, prepared, tc, i)
		})
	}
	wg.Wait()

	return results
}

// loadTestCaseData resolves file paths to actual content strings.
// Modifies testCase in-place, converting file paths to text.
func (s *JudgeEngine) loadTestCaseData(testCase *model.JudgeTestCase) error {
	if testCase.InputFile == "" {
		return nil
	}
	if s.externalFS == nil {
		return errors.New("external resources not configured, cannot load testcase files")
	}

	input, err := fs.ReadFile(s.externalFS, testCase.InputFile)
	if err != nil {
		return fmt.Errorf("load inputFile %q: %w", testCase.InputFile, err)
	}
	expectedOutput, err := fs.ReadFile(s.externalFS, testCase.ExpectedOutputFile)
	if err != nil {
		return fmt.Errorf("load expectedOutputFile %q: %w", testCase.ExpectedOutputFile, err)
	}

	testCase.InputText = string(input)
	testCase.ExpectedOutput = string(expectedOutput)
	testCase.InputFile = ""
	testCase.ExpectedOutputFile = ""
	return nil
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

func runSingleCase(
	ctx context.Context,
	req model.JudgeRequest,
	program compiledProgram,
	prepared preparedChecker,
	testCase model.JudgeTestCase,
	index int,
) model.JudgeCaseResult {
	runResult, err := program.Run(ctx, testCase.InputText, req.TimeLimit, req.MemoryLimit)
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

	checkResult, err := prepared.Check(ctx, testCase.InputText, runResult.Stdout, testCase.ExpectedOutput)
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
		switch checkResult.Verdict {
		case model.VerdictWA:
			message = "checker reported wrong answer"
		case model.VerdictUKE:
			message = "checker reported infrastructure failure"
		}
	}

	return judgeCaseResultFromExecution(runResult, checkResult.Verdict, message)
}

func failedBeforeRun(log string) model.JudgeResult {
	return model.JudgeResult{
		Status:  model.JudgeStatusSystemError,
		Compile: model.CompileResult{Succeeded: false, Log: log},
		Cases:   []model.JudgeCaseResult{},
	}
}

func judgeCaseResultFromExecution(
	runResult RunResult,
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

// aggregateStatus returns the overall system-level status of a judge session.
// It only reflects whether the judge infrastructure worked correctly:
//   - SystemError if any case has an infrastructure error
//   - OK otherwise (the per-case verdicts carry AC/WA/TLE/etc. details)
func aggregateStatus(cases []model.JudgeCaseResult) model.JudgeStatus {
	for _, c := range cases {
		if c.Verdict == model.VerdictUKE {
			return model.JudgeStatusSystemError
		}
	}
	return model.JudgeStatusOK
}
