// Package execution runs generic commands in a prepared workspace.
package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"
)

// ArtifactSpec describes one file to collect from the workspace after execution.
type ArtifactSpec struct {
	Name string
}

// Artifact is a file produced by an execution job.
type Artifact struct {
	Data []byte
	Mode os.FileMode
}

// Limits defines resource constraints for an execution job.
type Limits struct {
	CPUTimeMs   int
	WallTimeMs  int
	MemoryMB    int
	OutputBytes int64
}

// Verdict classifies the raw execution outcome.
type Verdict int

// Execution verdicts.
const (
	VerdictOK Verdict = iota
	VerdictTLE
	VerdictMLE
	VerdictOLE
	VerdictRE
)

// VerdictUnknown represents an unexpected verdict from the sandbox backend.
const VerdictUnknown Verdict = -1

// Job describes a single command executed in a temporary workspace.
type Job struct {
	Files         []workspace.File
	ImageRef      string
	Command       []string
	MountPath     string
	ReadOnlyMount bool
	Cwd           string
	Stdin         io.Reader
	Limits        Limits
	EnableSeccomp bool
	Artifacts     []ArtifactSpec
}

// Result contains the raw sandbox result and any collected artifacts.
type Result struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	CPUTimeMs int
	MemoryMB  int
	Verdict   Verdict
	ExtraInfo string
	Artifacts map[string]Artifact
}

// Default execution policy values shared by compile and run primitives.
const (
	// WallTimeMultiplier turns a CPU time limit into a wall-clock deadline.
	// Wall time accounts for I/O waits, scheduling latency, and container overhead.
	WallTimeMultiplier = 3

	// DefaultRunOutputLimitBytes caps user program and checker output.
	DefaultRunOutputLimitBytes = 16 * 1024 * 1024 // 16MB

	// DefaultCompileOutputLimitBytes caps compiler diagnostics.
	DefaultCompileOutputLimitBytes = 1 * 1024 * 1024 // 1MB
)

// Executor runs generic execution jobs.
type Executor interface {
	PreflightCheck(ctx context.Context) error
	Execute(ctx context.Context, job Job) (Result, error)
}

type executor struct {
	sandbox sandbox.Sandbox
}

// NewExecutor creates an executor backed by a sandbox.
func NewExecutor(sb sandbox.Sandbox) Executor {
	return &executor{sandbox: sb}
}

func (e *executor) PreflightCheck(ctx context.Context) error {
	return e.sandbox.PreflightCheck(ctx)
}

func (e *executor) Execute(ctx context.Context, job Job) (Result, error) {
	if err := validateJob(job); err != nil {
		return Result{}, err
	}

	ws, err := workspace.New()
	if err != nil {
		return Result{}, fmt.Errorf("create workspace: %w", err)
	}
	defer func() { _ = ws.Cleanup() }()

	if err := ws.WriteFiles(job.Files); err != nil {
		return Result{}, fmt.Errorf("write execution files: %w", err)
	}

	sandboxReq := sandbox.ExecuteRequest{
		ImageRef: job.ImageRef,
		Command:  job.Command,
		MountDir: &sandbox.Mount{
			HostPath:      ws.Dir(),
			ContainerPath: job.MountPath,
			ReadOnly:      job.ReadOnlyMount,
		},
		Stdin:         job.Stdin,
		Limits:        sandboxLimits(job.Limits),
		EnableSeccomp: job.EnableSeccomp,
	}

	if strings.TrimSpace(job.Cwd) != "" {
		cwd := job.Cwd
		sandboxReq.Cwd = &cwd
	}

	sandboxResult, err := e.sandbox.Execute(ctx, sandboxReq)
	if err != nil {
		return Result{}, fmt.Errorf("sandbox execute: %w", err)
	}

	result := Result{
		ExitCode:  sandboxResult.ExitCode,
		Stdout:    sandboxResult.Stdout,
		Stderr:    sandboxResult.Stderr,
		CPUTimeMs: sandboxResult.CPUTimeMs,
		MemoryMB:  sandboxResult.MemoryMB,
		Verdict:   verdictFromSandbox(sandboxResult.Verdict),
		ExtraInfo: sandboxResult.ExtraInfo,
	}

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

func verdictFromSandbox(verdict sandbox.Verdict) Verdict {
	switch verdict {
	case sandbox.VerdictOK:
		return VerdictOK
	case sandbox.VerdictTLE:
		return VerdictTLE
	case sandbox.VerdictMLE:
		return VerdictMLE
	case sandbox.VerdictOLE:
		return VerdictOLE
	case sandbox.VerdictRE:
		return VerdictRE
	default:
		return VerdictUnknown
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

func collectArtifacts(ws *workspace.Workspace, specs []ArtifactSpec) (map[string]Artifact, error) {
	artifacts := make(map[string]Artifact, len(specs))
	for _, spec := range specs {
		if strings.TrimSpace(spec.Name) == "" {
			return nil, errors.New("artifact name is required")
		}

		info, err := ws.Stat(spec.Name)
		if err != nil {
			return nil, fmt.Errorf("stat artifact %q: %w", spec.Name, err)
		}

		data, err := ws.ReadFile(spec.Name)
		if err != nil {
			return nil, fmt.Errorf("read artifact %q: %w", spec.Name, err)
		}

		artifacts[spec.Name] = Artifact{
			Data: data,
			Mode: info.Mode().Perm(),
		}
	}
	return artifacts, nil
}
