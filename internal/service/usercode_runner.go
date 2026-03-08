package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
)

// UserCodeRunner executes a compiled user program.
type UserCodeRunner interface {
	PreflightCheck(ctx context.Context) error
	Execute(ctx context.Context, req model.ExecuteRequest) (model.ExecuteResult, error)
}

type userCodeRunner struct {
	runner   Runner
	profiles map[model.Language]RunConfig
	log      *slog.Logger
}

// NewUserCodeRunner creates a user code runner with default language profiles.
func NewUserCodeRunner(runner Runner) UserCodeRunner {
	profiles := make(map[model.Language]RunConfig)

	profiles[model.LanguageC] = cProfile().Run
	profiles[model.LanguageCPP] = cppProfile().Run
	profiles[model.LanguageJava] = javaProfile().Run
	profiles[model.LanguagePython] = pythonProfile().Run

	return &userCodeRunner{
		runner:   runner,
		profiles: profiles,
		log:      slog.Default(),
	}
}

// PreflightCheck verifies backend runtime readiness.
func (r *userCodeRunner) PreflightCheck(ctx context.Context) error {
	return r.runner.PreflightCheck(ctx)
}

// Execute runs the given request and returns the execution result.
func (r *userCodeRunner) Execute(ctx context.Context, req model.ExecuteRequest) (model.ExecuteResult, error) {
	result, err := r.execute(ctx, req)
	if err != nil {
		r.log.ErrorContext(ctx, "execution failed", "error", err)
		return model.ExecuteResult{}, err
	}
	r.log.InfoContext(ctx, "execution complete",
		"verdict", result.Verdict.String(),
		"timeUsed", result.TimeUsed,
		"memoryUsed", result.MemoryUsed,
	)
	return result, nil
}

func (r *userCodeRunner) execute(ctx context.Context, req model.ExecuteRequest) (model.ExecuteResult, error) {
	profile, ok := r.profiles[req.Language]
	if !ok {
		return model.ExecuteResult{}, fmt.Errorf("no run profile for language %q", req.Language)
	}
	if len(req.Program.Data) == 0 {
		return model.ExecuteResult{}, errors.New("program data is required")
	}

	programMode := req.Program.Mode
	if programMode == 0 {
		programMode = profile.FileMode
	}

	containerPath := runMountDir + "/" + profile.ArtifactName
	runOut, err := r.runner.Run(ctx, RunRequest{
		Files: []RunFile{{
			Name:    profile.ArtifactName,
			Content: req.Program.Data,
			Mode:    programMode,
		}},
		ImageRef: profile.ImageRef,
		Command:  profile.RuntimeCommand(containerPath),
		Cwd:      runMountDir,
		Stdin:    strings.NewReader(req.Input),
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   req.TimeLimit,
			WallTimeMs:  req.TimeLimit * sandbox.WallTimeMultiplier,
			MemoryMB:    req.MemoryLimit,
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
