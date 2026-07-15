// Package service implements the core execution logic.
package service

import (
	"context"
	"io"

	"afterglow-judge-engine/internal/execution"
)

// RunRequest contains a generic run job definition.
type RunRequest struct {
	Files    []execution.File
	ImageRef string
	Command  []string
	Stdin    io.Reader
	Limits   execution.Limits
}

// RunResult contains the raw execution outcome from the runner primitive.
type RunResult = execution.RawResult

// Runner executes generic commands inside a sandboxed container.
type Runner interface {
	Run(ctx context.Context, req RunRequest) (RunResult, error)
}

// runner executes files in isolated containers.
type runner struct {
	executor execution.Executor
}

func newRunner(executor execution.Executor) Runner {
	return &runner{executor: executor}
}

// Run executes the given request and returns the raw execution result.
func (r *runner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	result, err := r.executor.Execute(ctx, execution.Job{
		Files:         req.Files,
		ImageRef:      req.ImageRef,
		Command:       req.Command,
		MountPath:     "/sandbox",
		ReadOnlyMount: true,
		Stdin:         req.Stdin,
		Limits:        req.Limits,
		EnableSeccomp: true, // User code execution requires seccomp restrictions
	})
	if err != nil {
		return RunResult{}, err
	}

	return result.RawResult, nil
}
