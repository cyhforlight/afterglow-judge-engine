// Package service implements the core execution logic using containerd.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
)

// ContainerdRunner executes code in isolated containers using containerd.
type ContainerdRunner struct {
	sandbox  sandbox.Sandbox
	profiles map[model.Language]sandbox.RunConfig
	log      *slog.Logger
}

// NewContainerdRunner creates a runner with default language profiles.
func NewContainerdRunner(sb sandbox.Sandbox) *ContainerdRunner {
	profiles := make(map[model.Language]sandbox.RunConfig)

	profiles[model.LanguageC] = sandbox.CProfile().Run
	profiles[model.LanguageCPP] = sandbox.CPPProfile().Run
	profiles[model.LanguageJava] = sandbox.JavaProfile().Run
	profiles[model.LanguagePython] = sandbox.PythonProfile().Run

	return &ContainerdRunner{
		sandbox:  sb,
		profiles: profiles,
		log:      slog.Default(),
	}
}

// PreflightCheck verifies that cgroup v2 and containerd are available.
func (r *ContainerdRunner) PreflightCheck(ctx context.Context) error {
	return r.sandbox.PreflightCheck(ctx)
}

// Execute runs the given request and returns the execution result.
func (r *ContainerdRunner) Execute(ctx context.Context, req model.ExecuteRequest) model.ExecuteResult {
	result, err := r.execute(ctx, req)
	if err != nil {
		r.log.ErrorContext(ctx, "execution failed", "error", err)
		return buildInfraFailureResult(err)
	}
	r.log.InfoContext(ctx, "execution complete",
		"verdict", result.Verdict.String(),
		"timeUsed", result.TimeUsed,
		"memoryUsed", result.MemoryUsed,
	)
	return result
}

func (r *ContainerdRunner) execute(ctx context.Context, req model.ExecuteRequest) (model.ExecuteResult, error) {
	profile, ok := r.profiles[req.Language]
	if !ok {
		return model.ExecuteResult{}, fmt.Errorf("no run profile for language %q", req.Language)
	}
	if len(req.Program.Data) == 0 {
		return model.ExecuteResult{}, fmt.Errorf("program data is required")
	}

	ws, err := NewWorkspace()
	if err != nil {
		return model.ExecuteResult{}, fmt.Errorf("create workspace: %w", err)
	}
	defer func() { _ = ws.Cleanup() }()

	programMode := req.Program.Mode
	if programMode == 0 {
		programMode = profile.FileMode
	}
	if err := ws.WriteFile(profile.ArtifactName, req.Program.Data, programMode); err != nil {
		return model.ExecuteResult{}, fmt.Errorf("write program file: %w", err)
	}

	containerPath := "/sandbox/" + profile.ArtifactName
	args := profile.RuntimeCommand(containerPath)
	cwd := "/sandbox"

	sandboxReq := sandbox.ExecuteRequest{
		ImageRef: profile.ImageRef,
		Command:  args,
		MountDir: &sandbox.Mount{
			HostPath:      ws.Dir(),
			ContainerPath: "/sandbox",
			ReadOnly:      true,
		},
		Cwd:   &cwd,
		Stdin: strings.NewReader(req.Input),
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   req.TimeLimit,
			WallTimeMs:  req.TimeLimit * sandbox.WallTimeMultiplier,
			MemoryMB:    req.MemoryLimit,
			OutputBytes: sandbox.DefaultExecutionOutputLimitBytes,
		},
	}

	result, err := r.sandbox.Execute(ctx, sandboxReq)
	if err != nil {
		return model.ExecuteResult{}, fmt.Errorf("sandbox execute: %w", err)
	}

	return convertSandboxResult(result), nil
}

func convertSandboxResult(sr sandbox.ExecuteResult) model.ExecuteResult {
	return model.ExecuteResult{
		Verdict:    convertVerdict(sr.Verdict),
		Stdout:     sr.Stdout,
		TimeUsed:   sr.CPUTimeMs,
		MemoryUsed: sr.MemoryMB,
		ExitCode:   sr.ExitCode,
		ExtraInfo:  sr.ExtraInfo,
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

func buildInfraFailureResult(err error) model.ExecuteResult {
	return model.ExecuteResult{
		Verdict:   model.VerdictUKE,
		ExitCode:  -1,
		ExtraInfo: err.Error(),
	}
}
