// Package service implements the core execution logic.
package service

import (
	"context"
	"errors"
	"io"
	"strings"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/workspace"
)

const runMountDir = "/sandbox"

// RunRequest contains a generic run job definition.
type RunRequest struct {
	Files    []workspace.File
	ImageRef string
	Command  []string
	Cwd      string
	Stdin    io.Reader
	Limits   execution.Limits
}

// RunResult contains the raw execution outcome from the runner primitive.
type RunResult = execution.RawResult

// Runner executes generic commands inside a sandboxed container.
type Runner interface {
	PreflightCheck(ctx context.Context) error
	Run(ctx context.Context, req RunRequest) (RunResult, error)
}

// runner executes files in isolated containers.
type runner struct {
	executor execution.Executor
}

// NewRunner creates a generic runner primitive.
func NewRunner(executor execution.Executor) Runner {
	return &runner{executor: executor}
}

// PreflightCheck verifies that cgroup v2 and containerd are available.
func (r *runner) PreflightCheck(ctx context.Context) error {
	return r.executor.PreflightCheck(ctx)
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

	cwd := req.Cwd
	if strings.TrimSpace(cwd) == "" {
		cwd = runMountDir
	}

	result, err := r.executor.Execute(ctx, execution.Job{
		Files:         req.Files,
		ImageRef:      req.ImageRef,
		Command:       req.Command,
		MountPath:     runMountDir,
		ReadOnlyMount: true,
		Cwd:           cwd,
		Stdin:         req.Stdin,
		Limits:        req.Limits,
		EnableSeccomp: true, // User code execution requires seccomp restrictions
	})
	if err != nil {
		return RunResult{}, err
	}

	return result.RawResult, nil
}
