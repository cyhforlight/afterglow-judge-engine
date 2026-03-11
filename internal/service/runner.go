// Package service implements the core execution logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"afterglow-judge-sandbox/internal/sandbox"
	"afterglow-judge-sandbox/internal/workspace"
)

const runMountDir = "/sandbox"

// RunFile is a single file written into the run workspace.
type RunFile struct {
	Name    string
	Content []byte
	Mode    os.FileMode
}

// RunRequest contains a generic run job definition.
type RunRequest struct {
	Files    []RunFile
	ImageRef string
	Command  []string
	Cwd      string
	Stdin    io.Reader
	Limits   sandbox.ResourceLimits
}

// RunResult contains the raw execution outcome from the runner primitive.
type RunResult struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	CPUTimeMs int
	MemoryMB  int
	Verdict   sandbox.Verdict
	ExtraInfo string
}

// Runner executes generic commands inside a sandboxed container.
type Runner interface {
	PreflightCheck(ctx context.Context) error
	Run(ctx context.Context, req RunRequest) (RunResult, error)
}

// runner executes files in isolated containers.
type runner struct {
	sandbox sandbox.Sandbox
}

// NewRunner creates a generic runner primitive.
func NewRunner(sb sandbox.Sandbox) Runner {
	return &runner{sandbox: sb}
}

// PreflightCheck verifies that cgroup v2 and containerd are available.
func (r *runner) PreflightCheck(ctx context.Context) error {
	return r.sandbox.PreflightCheck(ctx)
}

// Run executes the given request and returns the raw execution result.
func (r *runner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	if strings.TrimSpace(req.ImageRef) == "" {
		return RunResult{}, errors.New("run image is required")
	}
	if len(req.Command) == 0 {
		return RunResult{}, errors.New("run command is required")
	}
	if len(req.Files) == 0 {
		return RunResult{}, errors.New("at least one run file is required")
	}

	ws, err := workspace.New()
	if err != nil {
		return RunResult{}, fmt.Errorf("create workspace: %w", err)
	}
	defer func() { _ = ws.Cleanup() }()

	for _, file := range req.Files {
		fileMode := file.Mode
		if fileMode == 0 {
			fileMode = 0644
		}
		if err := ws.WriteFile(file.Name, file.Content, fileMode); err != nil {
			return RunResult{}, fmt.Errorf("write run file %q: %w", file.Name, err)
		}
	}

	cwd := req.Cwd
	if strings.TrimSpace(cwd) == "" {
		cwd = runMountDir
	}

	result, err := r.sandbox.Execute(ctx, sandbox.ExecuteRequest{
		ImageRef: req.ImageRef,
		Command:  req.Command,
		MountDir: &sandbox.Mount{
			HostPath:      ws.Dir(),
			ContainerPath: runMountDir,
			ReadOnly:      true,
		},
		Cwd:   &cwd,
		Stdin: req.Stdin,
		Limits: req.Limits,
	})
	if err != nil {
		return RunResult{}, fmt.Errorf("sandbox execute: %w", err)
	}

	return RunResult{
		ExitCode:  result.ExitCode,
		Stdout:    result.Stdout,
		Stderr:    result.Stderr,
		CPUTimeMs: result.CPUTimeMs,
		MemoryMB:  result.MemoryMB,
		Verdict:   result.Verdict,
		ExtraInfo: result.ExtraInfo,
	}, nil
}
