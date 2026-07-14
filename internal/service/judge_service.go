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
	"afterglow-judge-engine/internal/workspace"

	"golang.org/x/sync/semaphore"
)

// JudgeService handles full judge orchestration.
type JudgeService interface {
	PreflightCheck(ctx context.Context) error
	ValidateRequest(ctx context.Context, req model.JudgeRequest) error
	Judge(ctx context.Context, req model.JudgeRequest) model.JudgeResult
}

// JudgeEngine implements JudgeService.
type JudgeEngine struct {
	compiler       Compiler
	runner         Runner
	checker        checker
	externalFS     fs.FS
	concurrencySem *semaphore.Weighted
	limits         model.JudgeLimits
}

// NewJudgeEngine creates a judge engine.
func NewJudgeEngine(
	compiler Compiler,
	runner Runner,
	bundledFS fs.FS,
	externalFS fs.FS,
	maxConcurrent int,
	limits model.JudgeLimits,
) (*JudgeEngine, error) {
	checkerModule, err := newChecker(compiler, runner, bundledFS, externalFS)
	if err != nil {
		return nil, fmt.Errorf("initialize checker: %w", err)
	}

	return newJudgeEngine(compiler, runner, checkerModule, externalFS, maxConcurrent, limits), nil
}

func newJudgeEngine(
	compiler Compiler,
	runner Runner,
	checkerModule checker,
	externalFS fs.FS,
	maxConcurrent int,
	limits model.JudgeLimits,
) *JudgeEngine {
	if limits == (model.JudgeLimits{}) {
		limits = model.DefaultJudgeLimits()
	}

	return &JudgeEngine{
		compiler:       compiler,
		runner:         runner,
		checker:        checkerModule,
		externalFS:     externalFS,
		concurrencySem: semaphore.NewWeighted(int64(maxConcurrent)),
		limits:         limits,
	}
}

// PreflightCheck verifies backend runtime readiness.
func (s *JudgeEngine) PreflightCheck(ctx context.Context) error {
	return s.runner.PreflightCheck(ctx)
}

// ValidateRequest verifies whether the request can be handled by the judge.
func (s *JudgeEngine) ValidateRequest(_ context.Context, req model.JudgeRequest) error {
	if err := model.ValidateJudgeRequest(req, s.limits); err != nil {
		return err
	}

	resolved, err := s.checker.Resolve(req.Checker)
	if err != nil {
		return err
	}

	if err := resolved.Validate(); err != nil {
		return err
	}

	for index, testCase := range req.TestCases {
		if testCase.InputFile != "" {
			if err := s.validateExternalDependency(testCase.InputFile, "inputFile"); err != nil {
				return fmt.Errorf("testcases[%d]: %w", index, err)
			}
		}
		if testCase.ExpectedOutputFile != "" {
			if err := s.validateExternalDependency(testCase.ExpectedOutputFile, "expectedOutputFile"); err != nil {
				return fmt.Errorf("testcases[%d]: %w", index, err)
			}
		}
	}

	return nil
}

func (s *JudgeEngine) validateExternalDependency(path, label string) error {
	if s.externalFS == nil {
		return fmt.Errorf("%s %q requires external resources", label, path)
	}
	if _, err := fs.Stat(s.externalFS, path); err != nil {
		return fmt.Errorf("%s %q is not available: %w", label, path, err)
	}
	return nil
}

// Judge compiles source code and evaluates all test cases.
func (s *JudgeEngine) Judge(ctx context.Context, req model.JudgeRequest) model.JudgeResult {
	if err := model.ValidateJudgeRequest(req, s.limits); err != nil {
		return failedBeforeRun(req.TestCases, err.Error())
	}

	if err := s.concurrencySem.Acquire(ctx, 1); err != nil {
		return failedBeforeRun(req.TestCases, "judge request cancelled or timed out while waiting for capacity")
	}
	defer s.concurrencySem.Release(1)

	// Resolve checker before compilation so direct callers get early validation.
	resolved, err := s.checker.Resolve(req.Checker)
	if err != nil {
		return failedBeforeRun(req.TestCases, err.Error())
	}

	compileOut, compileResult, err := s.compileUserCode(ctx, req.Language, req.SourceCode)
	if err != nil {
		slog.ErrorContext(ctx, "compile step failed", "error", err)
		return failedBeforeRun(req.TestCases, fmt.Sprintf("compile infrastructure error: %v", err))
	}

	if !compileResult.Succeeded {
		return model.JudgeResult{
			Status:     model.JudgeStatusCompileError,
			Compile:    compileResult,
			TotalCount: len(req.TestCases),
			Cases:      make([]model.JudgeCaseResult, 0, len(req.TestCases)),
		}
	}

	prepared, err := resolved.Prepare(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "checker setup failed", "error", err)
		return s.unknownJudgeResult(req.TestCases, compileResult, err.Error())
	}

	caseResults, passedCount := s.runAllCases(ctx, req, compileOut, prepared)

	return model.JudgeResult{
		Status:      aggregateStatus(caseResults),
		Compile:     compileResult,
		Cases:       caseResults,
		PassedCount: passedCount,
		TotalCount:  len(req.TestCases),
	}
}

// runAllCases loads test data and executes each case concurrently.
// Actual parallelism is bounded by the shared container semaphore.
func (s *JudgeEngine) runAllCases(
	ctx context.Context,
	req model.JudgeRequest,
	userArtifact *model.CompiledArtifact,
	prepared preparedChecker,
) ([]model.JudgeCaseResult, int) {
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
			results[i] = s.runSingleCase(ctx, req, userArtifact, prepared, tc, i)
		})
	}
	wg.Wait()

	passed := 0
	for _, r := range results {
		if r.Verdict == model.VerdictOK {
			passed++
		}
	}

	return results, passed
}

// loadTestCaseData resolves file paths to actual content strings.
// Modifies testCase in-place, converting file paths to text.
func (s *JudgeEngine) loadTestCaseData(testCase *model.JudgeTestCase) error {
	if testCase.InputFile != "" {
		if s.externalFS == nil {
			return fmt.Errorf("external resources not configured, cannot load inputFile: %s", testCase.InputFile)
		}
		data, err := fs.ReadFile(s.externalFS, testCase.InputFile)
		if err != nil {
			return fmt.Errorf("load inputFile %q: %w", testCase.InputFile, err)
		}
		testCase.InputText = string(data)
		testCase.InputFile = "" // Clear after loading
	}

	if testCase.ExpectedOutputFile != "" {
		if s.externalFS == nil {
			return fmt.Errorf("external resources not configured, cannot load expectedOutputFile: %s", testCase.ExpectedOutputFile)
		}
		data, err := fs.ReadFile(s.externalFS, testCase.ExpectedOutputFile)
		if err != nil {
			return fmt.Errorf("load expectedOutputFile %q: %w", testCase.ExpectedOutputFile, err)
		}
		testCase.ExpectedOutput = string(data)
		testCase.ExpectedOutputFile = "" // Clear after loading
	}

	return nil
}

// compileUserCode compiles user source code to a runnable artifact.
func (s *JudgeEngine) compileUserCode(
	ctx context.Context,
	lang model.Language,
	sourceCode string,
) (*model.CompiledArtifact, model.CompileResult, error) {
	profile, err := ProfileForLanguage(lang)
	if err != nil {
		return nil, model.CompileResult{}, fmt.Errorf("get language profile: %w", err)
	}

	compileReq := CompileRequest{
		Files: []workspace.File{{
			Name:    profile.Compile.SourceFile,
			Content: []byte(sourceCode),
			Mode:    0o644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand,
		ArtifactName: profile.Compile.ArtifactName,
		Limits: execution.Limits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * execution.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: execution.DefaultCompileOutputLimitBytes,
		},
	}

	compileOut, err := s.compiler.Compile(ctx, compileReq)
	if err != nil {
		return nil, model.CompileResult{}, err
	}

	return compileOut.Artifact, compileOut.Result, nil
}

// executeUserCode runs compiled user code with given input and limits.
func (s *JudgeEngine) executeUserCode(
	ctx context.Context,
	artifact *model.CompiledArtifact,
	lang model.Language,
	input string,
	timeLimit int,
	memoryLimit int,
) (RunResult, error) {
	profile, err := ProfileForLanguage(lang)
	if err != nil {
		return RunResult{}, fmt.Errorf("get language profile: %w", err)
	}

	if artifact == nil || len(artifact.Data) == 0 {
		return RunResult{}, errors.New("program artifact is required")
	}

	containerPath := runMountDir + "/" + profile.Run.ArtifactName
	runOut, err := s.runner.Run(ctx, RunRequest{
		Files: []workspace.File{{
			Name:    profile.Run.ArtifactName,
			Content: artifact.Data,
			Mode:    artifact.Mode,
		}},
		ImageRef: profile.Run.ImageRef,
		Command:  profile.Run.RuntimeCommand(containerPath, memoryLimit),
		Stdin:    strings.NewReader(input),
		Limits: execution.Limits{
			CPUTimeMs:   timeLimit,
			WallTimeMs:  timeLimit * execution.WallTimeMultiplier,
			MemoryMB:    sandboxMemoryLimitMB(lang, memoryLimit),
			OutputBytes: execution.DefaultRunOutputLimitBytes,
		},
	})
	if err != nil {
		return RunResult{}, err
	}

	return normalizeUserRunResult(lang, runOut), nil
}

func normalizeUserRunResult(lang model.Language, runOut RunResult) RunResult {
	if lang == model.LanguageJava &&
		runOut.Verdict == execution.VerdictRE &&
		strings.Contains(runOut.Stderr, "java.lang.OutOfMemoryError") {
		runOut.Verdict = execution.VerdictMLE
	}
	return runOut
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

func (s *JudgeEngine) runSingleCase(
	ctx context.Context,
	req model.JudgeRequest,
	userArtifact *model.CompiledArtifact,
	prepared preparedChecker,
	testCase model.JudgeTestCase,
	index int,
) model.JudgeCaseResult {
	if userArtifact == nil {
		return model.JudgeCaseResult{
			Verdict:   model.VerdictUKE,
			ExtraInfo: "compiled artifact is missing",
		}
	}

	runResult, err := s.executeUserCode(ctx, userArtifact, req.Language, testCase.InputText, req.TimeLimit, req.MemoryLimit)
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

func (*JudgeEngine) unknownJudgeResult(
	testCases []model.JudgeTestCase,
	compileResult model.CompileResult,
	message string,
) model.JudgeResult {
	caseResults := make([]model.JudgeCaseResult, 0, len(testCases))
	for range testCases {
		caseResults = append(caseResults, model.JudgeCaseResult{
			Verdict:   model.VerdictUKE,
			ExtraInfo: message,
		})
	}

	return model.JudgeResult{
		Status:     model.JudgeStatusSystemError,
		Compile:    compileResult,
		Cases:      caseResults,
		TotalCount: len(testCases),
	}
}

func failedBeforeRun(testCases []model.JudgeTestCase, log string) model.JudgeResult {
	return model.JudgeResult{
		Status:     model.JudgeStatusSystemError,
		Compile:    model.CompileResult{Succeeded: false, Log: log},
		Cases:      []model.JudgeCaseResult{},
		TotalCount: len(testCases),
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
//   - SystemError if any case has an infrastructure error (or no cases at all)
//   - OK otherwise (the per-case verdicts carry AC/WA/TLE/etc. details)
func aggregateStatus(cases []model.JudgeCaseResult) model.JudgeStatus {
	if len(cases) == 0 {
		return model.JudgeStatusSystemError
	}
	for _, c := range cases {
		if c.Verdict == model.VerdictUKE {
			return model.JudgeStatusSystemError
		}
	}
	return model.JudgeStatusOK
}
