package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/storage"
)

// JudgeService handles full judge orchestration.
type JudgeService interface {
	PreflightCheck(ctx context.Context) error
	ValidateCheckerPolicy(ctx context.Context, req model.JudgeRequest) error
	Judge(ctx context.Context, req model.JudgeRequest) model.JudgeResult
}

// JudgeEngine implements JudgeService.
type JudgeEngine struct {
	runner          UserCodeRunner
	compiler        UserCodeCompiler
	checkerCompiler CheckerCompiler
	checkerRunner   CheckerRunner
	resources       ResourceStore
	externalStorage ResourceStore
	checkerPolicy   *CheckerPolicy
	log             *slog.Logger
}

// NewJudgeEngine creates a judge engine.
func NewJudgeEngine(
	runner UserCodeRunner,
	compiler UserCodeCompiler,
	checkerCompiler CheckerCompiler,
	checkerRunner CheckerRunner,
	resources ResourceStore,
	externalStorage *storage.ExternalStorage,
	checkerPolicy *CheckerPolicy,
) *JudgeEngine {
	return &JudgeEngine{
		runner:          runner,
		compiler:        compiler,
		checkerCompiler: checkerCompiler,
		checkerRunner:   checkerRunner,
		resources:       resources,
		externalStorage: externalStorage,
		checkerPolicy:   checkerPolicy,
		log:             slog.Default(),
	}
}

// PreflightCheck verifies backend runtime readiness.
func (s *JudgeEngine) PreflightCheck(ctx context.Context) error {
	return s.runner.PreflightCheck(ctx)
}

// ValidateCheckerPolicy verifies whether the request checker is allowed.
func (s *JudgeEngine) ValidateCheckerPolicy(_ context.Context, req model.JudgeRequest) error {
	_, err := s.resolveChecker(req.Checker)
	return err
}

// Judge compiles source code and evaluates all test cases.
func (s *JudgeEngine) Judge(ctx context.Context, req model.JudgeRequest) model.JudgeResult {
	if err := validateJudgeRequest(req); err != nil {
		return model.JudgeResult{
			Verdict: model.VerdictUKE,
			Compile: model.CompileResult{
				Succeeded: false,
				Log:       err.Error(),
			},
			TotalCount: len(req.TestCases),
		}
	}

	// Deep copy TestCases to avoid mutating caller's data
	testCases := make([]model.JudgeTestCase, len(req.TestCases))
	copy(testCases, req.TestCases)
	req.TestCases = testCases

	// Load test case files before compilation
	for i := range req.TestCases {
		if err := s.loadTestCaseData(ctx, &req.TestCases[i]); err != nil {
			s.log.ErrorContext(ctx, "failed to load test case data",
				"testCase", req.TestCases[i].Name, "error", err)
			return model.JudgeResult{
				Verdict: model.VerdictUKE,
				Compile: model.CompileResult{
					Succeeded: false,
					Log:       fmt.Sprintf("test data loading failed: %v", err),
				},
				TotalCount: len(req.TestCases),
			}
		}
	}

	// Resolve inside the service as well so direct callers cannot bypass checker policy.
	checkerLoc, err := s.resolveChecker(req.Checker)
	if err != nil {
		return model.JudgeResult{
			Verdict: model.VerdictUKE,
			Compile: model.CompileResult{
				Succeeded: false,
				Log:       err.Error(),
			},
			TotalCount: len(req.TestCases),
		}
	}

	compileOut, err := s.compiler.Compile(ctx, UserCodeCompileRequest{
		Language:   req.Language,
		SourceCode: req.SourceCode,
	})
	if err != nil {
		s.log.ErrorContext(ctx, "compile step failed", "error", err)
		return model.JudgeResult{
			Verdict: model.VerdictUKE,
			Compile: model.CompileResult{
				Succeeded: false,
				Log:       fmt.Sprintf("compile infrastructure error: %v", err),
			},
			TotalCount: len(req.TestCases),
		}
	}

	if !compileOut.Result.Succeeded {
		return model.JudgeResult{
			Verdict:    model.VerdictCE,
			Compile:    compileOut.Result,
			TotalCount: len(req.TestCases),
			Cases:      make([]model.JudgeCaseResult, 0, len(req.TestCases)),
		}
	}

	checkerOut, err := s.compileChecker(ctx, checkerLoc)
	if err != nil {
		s.log.ErrorContext(ctx, "checker setup failed", "error", err)
		return s.unknownJudgeResult(req.TestCases, compileOut.Result, fmt.Sprintf("checker setup failed: %v", err))
	}
	if !checkerOut.Result.Succeeded {
		message := strings.TrimSpace(checkerOut.Result.Log)
		if message == "" {
			message = "checker compilation failed"
		}
		s.log.ErrorContext(ctx, "checker compilation failed", "log", message)
		return s.unknownJudgeResult(req.TestCases, compileOut.Result, "checker compilation failed: "+message)
	}
	if checkerOut.Artifact == nil {
		s.log.ErrorContext(ctx, "checker compilation succeeded without artifact")
		return s.unknownJudgeResult(req.TestCases, compileOut.Result, "checker compilation succeeded without artifact")
	}

	caseResults := make([]model.JudgeCaseResult, 0, len(req.TestCases))
	passedCount := 0

	for _, testCase := range req.TestCases {
		caseResult := s.runSingleCase(ctx, req, compileOut, *checkerOut.Artifact, testCase)
		if caseResult.Verdict == model.VerdictOK {
			passedCount++
		}
		caseResults = append(caseResults, caseResult)
	}

	return model.JudgeResult{
		Verdict:     aggregateJudgeVerdict(caseResults),
		Compile:     compileOut.Result,
		Cases:       caseResults,
		PassedCount: passedCount,
		TotalCount:  len(req.TestCases),
	}
}

// loadTestCaseData resolves file paths to actual content strings.
// Modifies testCase in-place, converting file paths to text.
func (s *JudgeEngine) loadTestCaseData(ctx context.Context, testCase *model.JudgeTestCase) error {
	// Load input data
	if testCase.InputFile != "" {
		if s.externalStorage == nil {
			return fmt.Errorf("external storage not configured, cannot load inputFile: %s", testCase.InputFile)
		}
		data, err := s.externalStorage.Get(ctx, testCase.InputFile)
		if err != nil {
			return fmt.Errorf("load inputFile %q: %w", testCase.InputFile, err)
		}
		testCase.InputText = string(data)
		testCase.InputFile = "" // Clear after loading
	}

	// Load expected output data
	if testCase.ExpectedOutputFile != "" {
		if s.externalStorage == nil {
			return fmt.Errorf("external storage not configured, cannot load expectedOutputFile: %s", testCase.ExpectedOutputFile)
		}
		data, err := s.externalStorage.Get(ctx, testCase.ExpectedOutputFile)
		if err != nil {
			return fmt.Errorf("load expectedOutputFile %q: %w", testCase.ExpectedOutputFile, err)
		}
		testCase.ExpectedOutput = string(data)
		testCase.ExpectedOutputFile = "" // Clear after loading
	}

	return nil
}

func validateJudgeRequest(req model.JudgeRequest) error {
	if strings.TrimSpace(req.SourceCode) == "" {
		return errors.New("source code is required")
	}
	if req.Language == model.LanguageUnknown {
		return errors.New("language is required")
	}
	if req.TimeLimit <= 0 {
		return errors.New("time limit must be positive")
	}
	if req.MemoryLimit <= 0 {
		return errors.New("memory limit must be positive")
	}
	if len(req.TestCases) == 0 {
		return errors.New("at least one testcase is required")
	}
	return nil
}

func (s *JudgeEngine) compileChecker(ctx context.Context, loc CheckerLocation) (CheckerCompileOutput, error) {
	if s.checkerCompiler == nil {
		return CheckerCompileOutput{}, errors.New("checker compiler is required")
	}
	if s.checkerPolicy == nil {
		return CheckerCompileOutput{}, errors.New("checker policy is required")
	}

	var checkerSource []byte
	var err error

	if loc.IsExternal {
		if s.externalStorage == nil {
			return CheckerCompileOutput{}, errors.New("external storage not configured")
		}
		checkerSource, err = s.externalStorage.Get(ctx, loc.Path)
		if err != nil {
			return CheckerCompileOutput{}, fmt.Errorf("load external checker %q: %w", loc.Path, err)
		}
	} else {
		if s.resources == nil {
			return CheckerCompileOutput{}, errors.New("resource store is required")
		}
		storageKey := fmt.Sprintf("checkers/%s.cpp", loc.Path)
		checkerSource, err = s.resources.Get(ctx, storageKey)
		if err != nil {
			return CheckerCompileOutput{}, fmt.Errorf("load builtin checker %q from %q: %w", loc.Path, storageKey, err)
		}
	}

	testlibHeader, err := s.resources.Get(ctx, testlibHeaderKey)
	if err != nil {
		return CheckerCompileOutput{}, fmt.Errorf("load %q: %w", testlibHeaderKey, err)
	}

	return s.checkerCompiler.Compile(ctx, CheckerCompileRequest{
		SourceCode: checkerSource,
		SupportFiles: []CompileFile{{
			Name:    testlibHeaderKey,
			Content: testlibHeader,
			Mode:    0o644,
		}},
	})
}

func (s *JudgeEngine) resolveChecker(raw string) (CheckerLocation, error) {
	if s.checkerPolicy == nil {
		return CheckerLocation{}, errors.New("checker policy is required")
	}

	return s.checkerPolicy.Resolve(raw)
}

func (s *JudgeEngine) runSingleCase(
	ctx context.Context,
	req model.JudgeRequest,
	compileOut UserCodeCompileOutput,
	checkerArtifact model.CompiledArtifact,
	testCase model.JudgeTestCase,
) model.JudgeCaseResult {
	if compileOut.Artifact == nil {
		return model.JudgeCaseResult{
			Name:      testCase.Name,
			Verdict:   model.VerdictUKE,
			ExtraInfo: "compiled artifact is missing",
		}
	}

	runResult, err := s.runner.Execute(ctx, model.ExecuteRequest{
		Program:     *compileOut.Artifact,
		Input:       testCase.InputText,
		Language:    compileOut.RuntimeLanguage,
		TimeLimit:   req.TimeLimit,
		MemoryLimit: req.MemoryLimit,
	})
	if err != nil {
		s.log.ErrorContext(ctx, "program execution failed", "testCase", testCase.Name, "error", err)
		return model.JudgeCaseResult{
			Name:      testCase.Name,
			Verdict:   model.VerdictUKE,
			ExtraInfo: fmt.Sprintf("infrastructure error: %v", err),
		}
	}

	if runResult.Verdict != model.VerdictOK {
		return judgeCaseResultFromExecution(testCase.Name, runResult, runResult.Verdict, runResult.ExtraInfo)
	}
	if s.checkerRunner == nil {
		return judgeCaseResultFromExecution(testCase.Name, runResult, model.VerdictUKE, "checker runner is required")
	}

	checkerResult, err := s.checkerRunner.Run(ctx, CheckerRunRequest{
		Checker:        checkerArtifact,
		InputText:      testCase.InputText,
		ActualOutput:   runResult.Stdout,
		ExpectedOutput: testCase.ExpectedOutput,
	})
	if err != nil {
		s.log.ErrorContext(ctx, "checker execution failed", "testCase", testCase.Name, "error", err)
		return judgeCaseResultFromExecution(
			testCase.Name,
			runResult,
			model.VerdictUKE,
			fmt.Sprintf("checker infrastructure error: %v", err),
		)
	}

	return judgeCaseResultFromExecution(
		testCase.Name,
		runResult,
		checkerResult.Verdict,
		checkerMessageOrDefault(checkerResult),
	)
}

func (s *JudgeEngine) unknownJudgeResult(
	testCases []model.JudgeTestCase,
	compileResult model.CompileResult,
	message string,
) model.JudgeResult {
	caseResults := make([]model.JudgeCaseResult, 0, len(testCases))
	for _, testCase := range testCases {
		caseResults = append(caseResults, model.JudgeCaseResult{
			Name:      testCase.Name,
			Verdict:   model.VerdictUKE,
			ExtraInfo: message,
		})
	}

	return model.JudgeResult{
		Verdict:    model.VerdictUKE,
		Compile:    compileResult,
		Cases:      caseResults,
		TotalCount: len(testCases),
	}
}

func checkerMessageOrDefault(result CheckerRunResult) string {
	if result.Message != "" {
		return result.Message
	}
	if result.Verdict == model.VerdictWA {
		return "checker reported wrong answer"
	}
	if result.Verdict == model.VerdictUKE {
		return "checker reported infrastructure failure"
	}
	return ""
}

func judgeCaseResultFromExecution(
	caseName string,
	runResult model.ExecuteResult,
	verdict model.Verdict,
	extraInfo string,
) model.JudgeCaseResult {
	return model.JudgeCaseResult{
		Name:       caseName,
		Verdict:    verdict,
		Stdout:     runResult.Stdout,
		TimeUsed:   runResult.TimeUsed,
		MemoryUsed: runResult.MemoryUsed,
		ExitCode:   runResult.ExitCode,
		ExtraInfo:  extraInfo,
	}
}

func aggregateJudgeVerdict(cases []model.JudgeCaseResult) model.Verdict {
	if len(cases) == 0 {
		return model.VerdictUKE
	}

	highestRuntime := model.VerdictUnknown
	hasWA := false

	for _, caseResult := range cases {
		if isRuntimeVerdict(caseResult.Verdict) {
			if runtimeSeverity(caseResult.Verdict) > runtimeSeverity(highestRuntime) {
				highestRuntime = caseResult.Verdict
			}
			continue
		}

		if caseResult.Verdict == model.VerdictWA {
			hasWA = true
		}
	}

	if highestRuntime != model.VerdictUnknown {
		return highestRuntime
	}
	if hasWA {
		return model.VerdictWA
	}
	return model.VerdictOK
}

func isRuntimeVerdict(verdict model.Verdict) bool {
	switch verdict {
	case model.VerdictOLE, model.VerdictMLE, model.VerdictTLE, model.VerdictRE, model.VerdictUKE:
		return true
	default:
		return false
	}
}

func runtimeSeverity(verdict model.Verdict) int {
	switch verdict {
	case model.VerdictOLE:
		return 5
	case model.VerdictMLE:
		return 4
	case model.VerdictTLE:
		return 3
	case model.VerdictRE:
		return 2
	case model.VerdictUKE:
		return 1
	default:
		return 0
	}
}
