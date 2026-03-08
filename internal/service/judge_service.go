package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"

	"afterglow-judge-sandbox/internal/model"
)

// JudgeService handles full judge orchestration.
type JudgeService interface {
	PreflightCheck(ctx context.Context) error
	Judge(ctx context.Context, req model.JudgeRequest) model.JudgeResult
}

// JudgeEngine implements JudgeService.
type JudgeEngine struct {
	runner   UserCodeRunner
	compiler UserCodeCompiler
	log      *slog.Logger
}

// NewJudgeEngine creates a judge engine.
func NewJudgeEngine(runner UserCodeRunner, compiler UserCodeCompiler) *JudgeEngine {
	return &JudgeEngine{
		runner:   runner,
		compiler: compiler,
		log:      slog.Default(),
	}
}

// PreflightCheck verifies backend runtime readiness.
func (s *JudgeEngine) PreflightCheck(ctx context.Context) error {
	return s.runner.PreflightCheck(ctx)
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

	caseResults := make([]model.JudgeCaseResult, 0, len(req.TestCases))
	passedCount := 0

	for _, testCase := range req.TestCases {
		caseResult := s.runSingleCase(ctx, req, compileOut, testCase)
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

func (s *JudgeEngine) runSingleCase(
	ctx context.Context,
	req model.JudgeRequest,
	compileOut UserCodeCompileOutput,
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
		return model.JudgeCaseResult{
			Name:      testCase.Name,
			Verdict:   model.VerdictUKE,
			ExtraInfo: fmt.Sprintf("infrastructure error: %v", err),
		}
	}

	finalVerdict := runResult.Verdict
	extraInfo := runResult.ExtraInfo
	if runResult.Verdict == model.VerdictOK && !outputsMatch(runResult.Stdout, testCase.ExpectedOutput) {
		finalVerdict = model.VerdictWA
		extraInfo = "stdout does not match expected output"
	}

	return model.JudgeCaseResult{
		Name:       testCase.Name,
		Verdict:    finalVerdict,
		Stdout:     runResult.Stdout,
		TimeUsed:   runResult.TimeUsed,
		MemoryUsed: runResult.MemoryUsed,
		ExitCode:   runResult.ExitCode,
		ExtraInfo:  extraInfo,
	}
}

func outputsMatch(actual, expected string) bool {
	return normalizeOutput(actual) == normalizeOutput(expected)
}

func normalizeOutput(content string) string {
	return strings.TrimRightFunc(content, unicode.IsSpace)
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
