// Package execution prepares workspaces and runs generic container jobs.
package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"afterglow-judge-engine/internal/sandbox"

	"golang.org/x/sync/semaphore"
)

// Artifact is a file produced by an execution job.
type Artifact struct {
	Data []byte
	Mode os.FileMode
}

// File describes a file available to an execution job.
type File struct {
	Name    string
	Content []byte
	Mode    os.FileMode
}

// Limits defines resource constraints for an execution job.
type Limits struct {
	CPUTimeMs   int
	WallTimeMs  int
	MemoryMB    int
	OutputBytes int64
}

// Verdict classifies the raw execution outcome.
type Verdict = sandbox.Verdict

// Execution verdicts.
const (
	VerdictOK  = sandbox.VerdictOK
	VerdictTLE = sandbox.VerdictTLE
	VerdictMLE = sandbox.VerdictMLE
	VerdictOLE = sandbox.VerdictOLE
	VerdictRE  = sandbox.VerdictRE
)

// Job describes a single command executed in a temporary workspace.
type Job struct {
	Files         []File
	ImageRef      string
	Command       []string
	MountPath     string
	ReadOnlyMount bool
	Stdin         io.Reader
	Limits        Limits
	EnableSeccomp bool
	Artifacts     []string
}

// RawResult contains the outcome reported by the sandbox.
type RawResult = sandbox.ExecuteResult

// Result contains the raw sandbox result and any collected artifacts.
type Result struct {
	RawResult
	Artifacts map[string]Artifact
}

// Default execution policy values shared by compile and run primitives.
const (
	// WallTimeMultiplier turns a CPU time limit into a task-lifetime deadline.
	// Wall time stops tasks whose CPU time does not advance, such as blocked or sleeping programs.
	WallTimeMultiplier = 3

	// DefaultRunOutputLimitBytes caps user program and checker output.
	DefaultRunOutputLimitBytes = 16 * 1024 * 1024 // 16MB

	// DefaultCompileOutputLimitBytes caps compiler diagnostics.
	DefaultCompileOutputLimitBytes = 1 * 1024 * 1024 // 1MB
)

// Executor runs generic execution jobs.
type Executor interface {
	Execute(ctx context.Context, job Job) (Result, error)
}

type sandboxExecutor interface {
	Execute(ctx context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error)
}

type executor struct {
	sandbox sandboxExecutor
	sem     *semaphore.Weighted
}

// NewExecutor creates a capacity-limited executor backed by a sandbox.
func NewExecutor(sb sandboxExecutor, maxConcurrent int) Executor {
	if maxConcurrent <= 0 {
		panic("max concurrent executions must be positive")
	}
	return &executor{
		sandbox: sb,
		sem:     semaphore.NewWeighted(int64(maxConcurrent)),
	}
}

func (e *executor) Execute(ctx context.Context, job Job) (result Result, err error) {
	if err := validateJob(job); err != nil {
		return Result{}, err
	}
	if err := e.sem.Acquire(ctx, 1); err != nil {
		return Result{}, err
	}
	defer e.sem.Release(1)

	ws, err := newWorkspace()
	if err != nil {
		return Result{}, fmt.Errorf("create workspace: %w", err)
	}
	defer func() {
		err = errors.Join(err, ws.cleanup())
	}()

	if err := ws.writeFiles(job.Files); err != nil {
		return Result{}, fmt.Errorf("write execution files: %w", err)
	}

	sandboxReq := sandbox.ExecuteRequest{
		ImageRef: job.ImageRef,
		Command:  job.Command,
		MountDir: &sandbox.Mount{
			HostPath:      ws.dir(),
			ContainerPath: job.MountPath,
			ReadOnly:      job.ReadOnlyMount,
		},
		Stdin:         job.Stdin,
		Limits:        sandboxLimits(job.Limits),
		EnableSeccomp: job.EnableSeccomp,
	}

	sandboxResult, err := e.sandbox.Execute(ctx, sandboxReq)
	if err != nil {
		return Result{}, fmt.Errorf("sandbox execute: %w", err)
	}

	result = Result{RawResult: sandboxResult}

	if len(job.Artifacts) == 0 || result.ExitCode != 0 || result.Verdict != VerdictOK {
		return result, nil
	}

	artifacts, err := collectArtifacts(ws, job.Artifacts)
	if err != nil {
		return Result{}, err
	}
	result.Artifacts = artifacts
	return result, nil
}

func sandboxLimits(limits Limits) sandbox.ResourceLimits {
	return sandbox.ResourceLimits{
		CPUTimeMs:   limits.CPUTimeMs,
		WallTimeMs:  limits.WallTimeMs,
		MemoryMB:    limits.MemoryMB,
		OutputBytes: limits.OutputBytes,
	}
}

func validateJob(job Job) error {
	if strings.TrimSpace(job.ImageRef) == "" {
		return errors.New("execution image is required")
	}
	if len(job.Command) == 0 {
		return errors.New("execution command is required")
	}
	if len(job.Files) == 0 {
		return errors.New("at least one execution file is required")
	}
	if strings.TrimSpace(job.MountPath) == "" {
		return errors.New("execution mount path is required")
	}
	return nil
}

func collectArtifacts(ws *workspace, names []string) (map[string]Artifact, error) {
	artifacts := make(map[string]Artifact, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return nil, errors.New("artifact name is required")
		}

		info, err := ws.stat(name)
		if err != nil {
			return nil, fmt.Errorf("stat artifact %q: %w", name, err)
		}

		data, err := ws.readFile(name)
		if err != nil {
			return nil, fmt.Errorf("read artifact %q: %w", name, err)
		}

		artifacts[name] = Artifact{
			Data: data,
			Mode: info.Mode().Perm(),
		}
	}
	return artifacts, nil
}
