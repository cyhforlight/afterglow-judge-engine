package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"afterglow-judge-engine/internal/cache"
	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/storage"
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
	runner          Runner
	resources       ResourceStore
	externalStorage ResourceStore
	defaultChecker  string
	cache           *cache.Cache
	log             *slog.Logger
}

// NewJudgeEngine creates a judge engine.
func NewJudgeEngine(
	compiler Compiler,
	runner Runner,
	resources ResourceStore,
	externalStorage *storage.ExternalStorage,
	defaultChecker string,
	cache *cache.Cache,
) (*JudgeEngine, error) {
	defaultChecker = strings.TrimSpace(defaultChecker)
	if defaultChecker == "" {
		defaultChecker = defaultCheckerName
	}
	if err := validateCheckerShortName(defaultChecker); err != nil {
		return nil, fmt.Errorf("default checker: %w", err)
	}
	var externalResourceStore ResourceStore
	if externalStorage != nil {
		externalResourceStore = externalStorage
	}
	return &JudgeEngine{
		compiler:        compiler,
		runner:          runner,
		resources:       resources,
		externalStorage: externalResourceStore,
		defaultChecker:  defaultChecker,
		cache:           cache,
		log:             slog.Default(),
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
		if s.resources == nil {
			return fmt.Errorf("builtin checker %q is not available: internal resource store is unavailable", checkerLoc.Path)
		}

		checkerSourceKey := fmt.Sprintf("checkers/%s.cpp", checkerLoc.Path)
		if err := s.resources.Stat(ctx, checkerSourceKey); err != nil {
			return fmt.Errorf("builtin checker %q is not available: %w", checkerLoc.Path, err)
		}
	}

	if s.resources == nil {
		return fmt.Errorf("checker dependency %q is not available: internal resource store is unavailable", testlibHeaderKey)
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
	if err := validateJudgeRequest(req); err != nil {
		return failedBeforeRun(req.TestCases, err.Error())
	}

	// Copy TestCases to avoid mutating caller's slice.
	// req is passed by value, but TestCases is a slice (reference type).
	testCases := make([]model.JudgeTestCase, len(req.TestCases))
	copy(testCases, req.TestCases)
	req.TestCases = testCases

	// Load test case files before compilation
	for i := range req.TestCases {
		if err := s.loadTestCaseData(ctx, &req.TestCases[i]); err != nil {
			s.log.ErrorContext(ctx, "failed to load test case data",
				"index", i, "error", err)
			return failedBeforeRun(req.TestCases, fmt.Sprintf("test data loading failed: %v", err))
		}
	}

	// Resolve checker before compilation so direct callers get early validation.
	checkerLoc, err := ResolveChecker(req.Checker, s.defaultChecker)
	if err != nil {
		return failedBeforeRun(req.TestCases, err.Error())
	}

	compileOut, compileResult, err := s.compileUserCode(ctx, req.Language, req.SourceCode)
	if err != nil {
		s.log.ErrorContext(ctx, "compile step failed", "error", err)
		return failedBeforeRun(req.TestCases, fmt.Sprintf("compile infrastructure error: %v", err))
	}

	if !compileResult.Succeeded {
		return model.JudgeResult{
			Verdict:    model.VerdictCE,
			Compile:    compileResult,
			TotalCount: len(req.TestCases),
			Cases:      make([]model.JudgeCaseResult, 0, len(req.TestCases)),
		}
	}

	checkerArtifact, checkerResult, err := s.prepareChecker(ctx, checkerLoc)
	if err != nil {
		s.log.ErrorContext(ctx, "checker setup failed", "error", err)
		return s.unknownJudgeResult(req.TestCases, compileResult, fmt.Sprintf("checker setup failed: %v", err))
	}
	if !checkerResult.Succeeded {
		message := strings.TrimSpace(checkerResult.Log)
		if message == "" {
			message = "checker compilation failed"
		}
		s.log.ErrorContext(ctx, "checker compilation failed", "log", message)
		return s.unknownJudgeResult(req.TestCases, compileResult, "checker compilation failed: "+message)
	}
	if checkerArtifact == nil {
		s.log.ErrorContext(ctx, "checker compilation succeeded without artifact")
		return s.unknownJudgeResult(req.TestCases, compileResult, "checker compilation succeeded without artifact")
	}

	caseResults := make([]model.JudgeCaseResult, 0, len(req.TestCases))
	passedCount := 0

	for i, testCase := range req.TestCases {
		caseResult := s.runSingleCase(ctx, req, compileOut, checkerArtifact, testCase, i)
		if caseResult.Verdict == model.VerdictOK {
			passedCount++
		}
		caseResults = append(caseResults, caseResult)
	}

	return model.JudgeResult{
		Verdict:     selectWorstVerdict(caseResults),
		Compile:     compileResult,
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
		ArtifactMode: profile.Run.FileMode,
		ArtifactPath: profile.Compile.ArtifactName,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	}

	// Python bytecode requires special artifact loading
	if lang == model.LanguagePython {
		compileReq.ArtifactLoader = loadPythonBytecodeArtifact(profile.Compile.ArtifactName, profile.Run.FileMode)
	}

	compileOut, err := s.compiler.Compile(ctx, compileReq)
	if err != nil {
		return nil, model.CompileResult{}, err
	}

	return compileOut.Artifact, compileOut.Result, nil
}

func loadPythonBytecodeArtifact(artifactName string, artifactMode os.FileMode) ArtifactLoader {
	return func(workDir string) (model.CompiledArtifact, error) {
		pycachePath := filepath.Join(workDir, "__pycache__")
		entries, err := os.ReadDir(pycachePath)
		if err != nil {
			return model.CompiledArtifact{}, fmt.Errorf("read python cache directory: %w", err)
		}

		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".pyc" {
				continue
			}

			artifact, err := loadCompiledArtifactAtPath(filepath.Join(pycachePath, entry.Name()))
			if err != nil {
				return model.CompiledArtifact{}, err
			}
			artifact.Name = artifactName
			if artifact.Mode == 0 {
				artifact.Mode = artifactMode
			}
			return artifact, nil
		}

		return model.CompiledArtifact{}, fmt.Errorf("python bytecode artifact not found in %q", pycachePath)
	}
}

// prepareChecker loads, compiles, and caches a checker.
func (s *JudgeEngine) prepareChecker(
	ctx context.Context,
	loc CheckerLocation,
) (*model.CompiledArtifact, model.CompileResult, error) {
	// Load checker source
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
		if s.resources == nil {
			return nil, model.CompileResult{}, errors.New("resource store is required")
		}
		storageKey := fmt.Sprintf("checkers/%s.cpp", loc.Path)
		checkerSource, err = s.resources.Get(ctx, storageKey)
		if err != nil {
			return nil, model.CompileResult{}, fmt.Errorf("load builtin checker %q from %q: %w", loc.Path, storageKey, err)
		}
	}

	// Check cache
	cacheKey := computeCheckerCacheKey(checkerSource)
	if s.cache != nil {
		if cached, ok := s.cache.Get(cacheKey); ok {
			s.log.InfoContext(ctx, "checker cache hit", "key", cacheKey[:16])
			return &model.CompiledArtifact{
				Name: checkerArtifactFileName,
				Data: cached,
				Mode: 0755,
			}, model.CompileResult{Succeeded: true}, nil
		}
		s.log.InfoContext(ctx, "checker cache miss", "key", cacheKey[:16])
	}

	// Load testlib.h
	testlibHeader, err := s.resources.Get(ctx, testlibHeaderKey)
	if err != nil {
		return nil, model.CompileResult{}, fmt.Errorf("load %q: %w", testlibHeaderKey, err)
	}

	// Compile checker
	profile := cppProfile()
	compileReq := CompileRequest{
		Files: []workspace.File{
			{
				Name:    checkerSourceFileName,
				Content: checkerSource,
				Mode:    0644,
			},
			{
				Name:    testlibHeaderKey,
				Content: testlibHeader,
				Mode:    0644,
			},
		},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand([]string{checkerSourceFileName}),
		ArtifactName: checkerArtifactFileName,
		ArtifactMode: profile.Run.FileMode,
		ArtifactPath: profile.Compile.ArtifactName,
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

	// Ensure artifact has correct name
	if compileOut.Artifact != nil {
		compileOut.Artifact.Name = checkerArtifactFileName
	}

	// Cache successful compilation
	if s.cache != nil && compileOut.Result.Succeeded && compileOut.Artifact != nil {
		s.cache.Set(cacheKey, compileOut.Artifact.Data)
	}

	return compileOut.Artifact, compileOut.Result, nil
}

func computeCheckerCacheKey(source []byte) string {
	hash := sha256.Sum256(source)
	return fmt.Sprintf("checker:%x", hash)
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

	programMode := artifact.Mode
	if programMode == 0 {
		programMode = profile.Run.FileMode
	}

	containerPath := runMountDir + "/" + profile.Run.ArtifactName
	runOut, err := s.runner.Run(ctx, RunRequest{
		Files: []workspace.File{{
			Name:    profile.Run.ArtifactName,
			Content: artifact.Data,
			Mode:    programMode,
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

	profile := cppProfile().Run
	checkerMode := checkerArtifact.Mode
	if checkerMode == 0 {
		checkerMode = profile.FileMode
	}

	runOut, err := s.runner.Run(ctx, RunRequest{
		Files: []workspace.File{
			{Name: checkerArtifactFileName, Content: checkerArtifact.Data, Mode: checkerMode},
			{Name: checkerInputFileName, Content: []byte(inputText), Mode: 0644},
			{Name: checkerOutputFileName, Content: []byte(actualOutput), Mode: 0644},
			{Name: checkerAnswerFileName, Content: []byte(expectedOutput), Mode: 0644},
		},
		ImageRef: profile.ImageRef,
		Command: []string{
			runMountDir + "/" + checkerArtifactFileName,
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
		s.log.ErrorContext(ctx, "program execution failed", "index", index, "error", err)
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
		s.log.ErrorContext(ctx, "checker execution failed", "index", index, "error", err)
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
		Verdict:    model.VerdictUKE,
		Compile:    compileResult,
		Cases:      caseResults,
		TotalCount: len(testCases),
	}
}

func failedBeforeRun(testCases []model.JudgeTestCase, log string) model.JudgeResult {
	return model.JudgeResult{
		Verdict:    model.VerdictUKE,
		Compile:    model.CompileResult{Succeeded: false, Log: log},
		TotalCount: len(testCases),
	}
}

func judgeCaseResultFromExecution(
	runResult model.ExecuteResult,
	verdict model.Verdict,
	extraInfo string,
) model.JudgeCaseResult {
	return model.JudgeCaseResult{
		Verdict:    verdict,
		Stdout:     runResult.Stdout,
		TimeUsed:   runResult.TimeUsed,
		MemoryUsed: runResult.MemoryUsed,
		ExitCode:   runResult.ExitCode,
		ExtraInfo:  extraInfo,
	}
}
