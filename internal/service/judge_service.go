package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"
)

// JudgeService handles full judge orchestration.
type JudgeService interface {
	PreflightCheck(ctx context.Context) error
	ValidateRequest(ctx context.Context, req model.JudgeRequest) error
	Judge(ctx context.Context, req model.JudgeRequest) model.JudgeResult
}

// JudgeEngine implements JudgeService.
type JudgeEngine struct {
	compiler        Compiler
	checkerCompiler Compiler
	runner          Runner
	resources       ResourceStore
	externalStorage ResourceStore
	defaultChecker  string
}

// NewJudgeEngine creates a judge engine.
func NewJudgeEngine(
	compiler Compiler,
	checkerCompiler Compiler,
	runner Runner,
	resources ResourceStore,
	externalStorage ResourceStore,
	defaultChecker string,
) (*JudgeEngine, error) {
	if resources == nil {
		return nil, errors.New("internal resource store is required")
	}

	defaultChecker = strings.TrimSpace(defaultChecker)
	if defaultChecker == "" {
		defaultChecker = defaultCheckerName
	}
	if err := validateCheckerShortName(defaultChecker); err != nil {
		return nil, fmt.Errorf("default checker: %w", err)
	}
	return &JudgeEngine{
		compiler:        compiler,
		checkerCompiler: checkerCompiler,
		runner:          runner,
		resources:       resources,
		externalStorage: externalStorage,
		defaultChecker:  defaultChecker,
	}, nil
}

// PreflightCheck verifies backend runtime readiness.
func (s *JudgeEngine) PreflightCheck(ctx context.Context) error {
	return s.runner.PreflightCheck(ctx)
}

// ValidateRequest verifies whether the request can be handled by the judge.
func (s *JudgeEngine) ValidateRequest(ctx context.Context, req model.JudgeRequest) error {
	checkerLoc, err := ResolveChecker(req.Checker, s.defaultChecker)
	if err != nil {
		return err
	}

	if err := s.validateCheckerDependencies(ctx, checkerLoc); err != nil {
		return err
	}

	for index, testCase := range req.TestCases {
		if testCase.InputFile != "" {
			if err := s.validateExternalDependency(ctx, testCase.InputFile, "inputFile"); err != nil {
				return fmt.Errorf("testcases[%d]: %w", index, err)
			}
		}
		if testCase.ExpectedOutputFile != "" {
			if err := s.validateExternalDependency(ctx, testCase.ExpectedOutputFile, "expectedOutputFile"); err != nil {
				return fmt.Errorf("testcases[%d]: %w", index, err)
			}
		}
	}

	return nil
}

func (s *JudgeEngine) validateCheckerDependencies(ctx context.Context, checkerLoc CheckerLocation) error {
	if checkerLoc.IsExternal {
		if err := s.validateExternalDependency(ctx, checkerLoc.Path, "external checker"); err != nil {
			return err
		}
	} else {
		checkerSourceKey := fmt.Sprintf("checkers/%s.cpp", checkerLoc.Path)
		if err := s.resources.Stat(ctx, checkerSourceKey); err != nil {
			return fmt.Errorf("builtin checker %q is not available: %w", checkerLoc.Path, err)
		}
	}

	if err := s.resources.Stat(ctx, testlibHeaderKey); err != nil {
		return fmt.Errorf("checker dependency %q is not available: %w", testlibHeaderKey, err)
	}

	return nil
}

func (s *JudgeEngine) validateExternalDependency(ctx context.Context, path, label string) error {
	if s.externalStorage == nil {
		return fmt.Errorf("%s %q requires external storage", label, path)
	}
	if err := s.externalStorage.Stat(ctx, path); err != nil {
		return fmt.Errorf("%s %q is not available: %w", label, path, err)
	}
	return nil
}

// Judge compiles source code and evaluates all test cases.
func (s *JudgeEngine) Judge(ctx context.Context, req model.JudgeRequest) model.JudgeResult {
	// Resolve checker before compilation so direct callers get early validation.
	checkerLoc, err := ResolveChecker(req.Checker, s.defaultChecker)
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

	checkerArtifact, checkerResult, err := s.prepareChecker(ctx, checkerLoc)
	if err != nil {
		slog.ErrorContext(ctx, "checker setup failed", "error", err)
		return s.unknownJudgeResult(req.TestCases, compileResult, fmt.Sprintf("checker setup failed: %v", err))
	}
	if !checkerResult.Succeeded {
		message := strings.TrimSpace(checkerResult.Log)
		if message == "" {
			message = "checker compilation failed"
		}
		slog.ErrorContext(ctx, "checker compilation failed", "log", message)
		return s.unknownJudgeResult(req.TestCases, compileResult, "checker compilation failed: "+message)
	}
	if checkerArtifact == nil {
		slog.ErrorContext(ctx, "checker compilation succeeded without artifact")
		return s.unknownJudgeResult(req.TestCases, compileResult, "checker compilation succeeded without artifact")
	}

	caseResults, passedCount := s.runAllCases(ctx, req, compileOut, checkerArtifact)

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
	checkerArtifact *model.CompiledArtifact,
) ([]model.JudgeCaseResult, int) {
	results := make([]model.JudgeCaseResult, len(req.TestCases))

	var wg sync.WaitGroup
	for i, tc := range req.TestCases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.loadTestCaseData(ctx, &tc); err != nil {
				slog.ErrorContext(ctx, "failed to load test case data", "index", i, "error", err)
				results[i] = model.JudgeCaseResult{
					Verdict:   model.VerdictUKE,
					ExtraInfo: fmt.Sprintf("test data loading failed: %v", err),
				}
				return
			}
			results[i] = s.runSingleCase(ctx, req, userArtifact, checkerArtifact, tc, i)
		}()
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
			Name:    profile.Compile.SourceFiles[0],
			Content: []byte(sourceCode),
			Mode:    0644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand(profile.Compile.SourceFiles),
		ArtifactName: profile.Compile.ArtifactName,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	}

	compileOut, err := s.compiler.Compile(ctx, compileReq)
	if err != nil {
		return nil, model.CompileResult{}, err
	}

	return compileOut.Artifact, compileOut.Result, nil
}

// prepareChecker loads checker source and testlib.h, then delegates compilation
// to the checkerCompiler (which handles caching and singleflight internally).
func (s *JudgeEngine) prepareChecker(
	ctx context.Context,
	loc CheckerLocation,
) (*model.CompiledArtifact, model.CompileResult, error) {
	// Load checker source.
	var checkerSource []byte
	var err error

	if loc.IsExternal {
		if s.externalStorage == nil {
			return nil, model.CompileResult{}, errors.New("external storage not configured")
		}
		checkerSource, err = s.externalStorage.Get(ctx, loc.Path)
		if err != nil {
			return nil, model.CompileResult{}, fmt.Errorf("load external checker %q: %w", loc.Path, err)
		}
	} else {
		storageKey := fmt.Sprintf("checkers/%s.cpp", loc.Path)
		checkerSource, err = s.resources.Get(ctx, storageKey)
		if err != nil {
			return nil, model.CompileResult{}, fmt.Errorf("load builtin checker %q from %q: %w", loc.Path, storageKey, err)
		}
	}

	// Load testlib.h.
	testlibHeader, err := s.resources.Get(ctx, testlibHeaderKey)
	if err != nil {
		return nil, model.CompileResult{}, fmt.Errorf("load %q: %w", testlibHeaderKey, err)
	}

	// Build compile request and delegate to checkerCompiler.
	profile := checkerProfile()
	compileOut, err := s.checkerCompiler.Compile(ctx, CompileRequest{
		Files: []workspace.File{
			{Name: checkerSourceFileName, Content: checkerSource, Mode: 0644},
			{Name: testlibHeaderKey, Content: testlibHeader, Mode: 0644},
		},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand([]string{checkerSourceFileName}),
		ArtifactName: profile.Compile.ArtifactName,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	})
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
) (model.ExecuteResult, error) {
	profile, err := ProfileForLanguage(lang)
	if err != nil {
		return model.ExecuteResult{}, fmt.Errorf("get language profile: %w", err)
	}

	if artifact == nil || len(artifact.Data) == 0 {
		return model.ExecuteResult{}, errors.New("program artifact is required")
	}

	containerPath := runMountDir + "/" + profile.Run.ArtifactName
	runOut, err := s.runner.Run(ctx, RunRequest{
		Files: []workspace.File{{
			Name:    profile.Run.ArtifactName,
			Content: artifact.Data,
			Mode:    artifact.Mode,
		}},
		ImageRef: profile.Run.ImageRef,
		Command:  profile.Run.RuntimeCommand(containerPath),
		Cwd:      runMountDir,
		Stdin:    strings.NewReader(input),
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   timeLimit,
			WallTimeMs:  timeLimit * sandbox.WallTimeMultiplier,
			MemoryMB:    memoryLimit,
			OutputBytes: sandbox.DefaultExecutionOutputLimitBytes,
		},
	})
	if err != nil {
		return model.ExecuteResult{}, err
	}

	return convertRunResult(runOut), nil
}

func convertRunResult(runOut RunResult) model.ExecuteResult {
	return model.ExecuteResult{
		Verdict:    convertVerdict(runOut.Verdict),
		Stdout:     runOut.Stdout,
		TimeUsed:   runOut.CPUTimeMs,
		MemoryUsed: runOut.MemoryMB,
		ExitCode:   runOut.ExitCode,
		ExtraInfo:  runOut.ExtraInfo,
	}
}

func convertVerdict(v sandbox.Verdict) model.Verdict {
	switch v {
	case sandbox.VerdictOK:
		return model.VerdictOK
	case sandbox.VerdictTLE:
		return model.VerdictTLE
	case sandbox.VerdictMLE:
		return model.VerdictMLE
	case sandbox.VerdictOLE:
		return model.VerdictOLE
	case sandbox.VerdictRE:
		return model.VerdictRE
	default:
		return model.VerdictUKE
	}
}

// runChecker executes the checker to validate user output.
func (s *JudgeEngine) runChecker(
	ctx context.Context,
	checkerArtifact *model.CompiledArtifact,
	inputText string,
	actualOutput string,
	expectedOutput string,
) (model.Verdict, string, error) {
	if checkerArtifact == nil || len(checkerArtifact.Data) == 0 {
		return model.VerdictUKE, "", errors.New("checker artifact is required")
	}

	profile := checkerProfile()
	runOut, err := s.runner.Run(ctx, RunRequest{
		Files: []workspace.File{
			{Name: profile.Run.ArtifactName, Content: checkerArtifact.Data, Mode: checkerArtifact.Mode},
			{Name: checkerInputFileName, Content: []byte(inputText), Mode: 0644},
			{Name: checkerOutputFileName, Content: []byte(actualOutput), Mode: 0644},
			{Name: checkerAnswerFileName, Content: []byte(expectedOutput), Mode: 0644},
		},
		ImageRef: profile.Run.ImageRef,
		Command: []string{
			runMountDir + "/" + profile.Run.ArtifactName,
			runMountDir + "/" + checkerInputFileName,
			runMountDir + "/" + checkerOutputFileName,
			runMountDir + "/" + checkerAnswerFileName,
		},
		Cwd:    runMountDir,
		Limits: checkerRunLimits(),
	})
	if err != nil {
		return model.VerdictUKE, "", err
	}

	// Extract message
	message := strings.TrimSpace(runOut.Stderr)
	if message == "" {
		message = strings.TrimSpace(runOut.Stdout)
	}
	if message == "" {
		message = strings.TrimSpace(runOut.ExtraInfo)
	}

	// Check for sandbox failures
	switch runOut.Verdict {
	case sandbox.VerdictTLE, sandbox.VerdictMLE, sandbox.VerdictOLE:
		return model.VerdictUKE, message, nil
	}

	// Parse exit code
	switch runOut.ExitCode {
	case 0:
		if runOut.Verdict != sandbox.VerdictOK {
			return model.VerdictUKE, message, nil
		}
		return model.VerdictOK, message, nil
	case 1, 2:
		return model.VerdictWA, message, nil
	default:
		return model.VerdictUKE, message, nil
	}
}

func checkerRunLimits() sandbox.ResourceLimits {
	return sandbox.ResourceLimits{
		CPUTimeMs:   checkerCPUTimeLimitMs,
		WallTimeMs:  checkerCPUTimeLimitMs * sandbox.WallTimeMultiplier,
		MemoryMB:    checkerMemoryLimitMB,
		OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
	}
}

func (s *JudgeEngine) runSingleCase(
	ctx context.Context,
	req model.JudgeRequest,
	userArtifact *model.CompiledArtifact,
	checkerArtifact *model.CompiledArtifact,
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

	if runResult.Verdict != model.VerdictOK {
		return judgeCaseResultFromExecution(runResult, runResult.Verdict, runResult.ExtraInfo)
	}

	checkerVerdict, checkerMessage, err := s.runChecker(ctx, checkerArtifact, testCase.InputText, runResult.Stdout, testCase.ExpectedOutput)
	if err != nil {
		slog.ErrorContext(ctx, "checker execution failed", "index", index, "error", err)
		return judgeCaseResultFromExecution(
			runResult,
			model.VerdictUKE,
			fmt.Sprintf("checker infrastructure error: %v", err),
		)
	}

	message := checkerMessage
	if message == "" {
		switch checkerVerdict {
		case model.VerdictWA:
			message = "checker reported wrong answer"
		case model.VerdictUKE:
			message = "checker reported infrastructure failure"
		}
	}

	return judgeCaseResultFromExecution(runResult, checkerVerdict, message)
}

func (s *JudgeEngine) unknownJudgeResult(
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
		TotalCount: len(testCases),
	}
}

func judgeCaseResultFromExecution(
	runResult model.ExecuteResult,
	verdict model.Verdict,
	extraInfo string,
) model.JudgeCaseResult {
	result := model.JudgeCaseResult(runResult)
	result.Verdict = verdict
	result.ExtraInfo = extraInfo
	return result
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
