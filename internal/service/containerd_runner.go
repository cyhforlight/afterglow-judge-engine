// Package service implements the core execution logic using containerd.
package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

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
func NewContainerdRunner(socketPath string) *ContainerdRunner {
	sb := sandbox.NewContainerdSandbox(socketPath)
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

// GetSandbox returns the underlying sandbox instance (for compiler initialization).
func (r *ContainerdRunner) GetSandbox() sandbox.Sandbox {
	return r.sandbox
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

	tmpDir, err := os.MkdirTemp("", "sandbox-exec-*")
	if err != nil {
		return model.ExecuteResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	programPath := filepath.Join(tmpDir, profile.ArtifactName)
	if err := copyFile(req.ExecutablePath, programPath, profile.FileMode); err != nil {
		return model.ExecuteResult{}, fmt.Errorf("copy program file: %w", err)
	}

	inputFile, err := os.Open(req.InputPath) //nolint:gosec // G304: path is from validated user input
	if err != nil {
		return model.ExecuteResult{}, fmt.Errorf("open input file: %w", err)
	}
	defer func() { _ = inputFile.Close() }()

	containerPath := "/sandbox/" + profile.ArtifactName
	args := profile.RuntimeCommand(containerPath)

	sandboxReq := sandbox.ExecuteRequest{
		ImageRef: profile.ImageRef,
		Command:  args,
		Mounts: []sandbox.Mount{{
			HostPath:      tmpDir,
			ContainerPath: "/sandbox",
			ReadOnly:      true,
		}},
		Stdin: inputFile,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   req.TimeLimit,
			WallTimeMs:  req.TimeLimit * 3,
			MemoryMB:    req.MemoryLimit,
			OutputBytes: 16 * 1024 * 1024, // 16MB
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

func copyFile(src, dst string, perm os.FileMode) error {
	data, err := os.ReadFile(src) //nolint:gosec // G304: paths are from validated configuration
	if err != nil {
		return fmt.Errorf("read source file %q: %w", src, err)
	}
	if err := os.WriteFile(dst, data, perm); err != nil {
		return fmt.Errorf("write destination file %q: %w", dst, err)
	}
	return nil
}

func buildInfraFailureResult(err error) model.ExecuteResult {
	return model.ExecuteResult{
		Verdict:   model.VerdictUKE,
		ExitCode:  -1,
		ExtraInfo: err.Error(),
	}
}
