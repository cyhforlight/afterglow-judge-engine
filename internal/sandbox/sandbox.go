// Package sandbox provides a unified container execution abstraction for both
// compilation and runtime execution.
package sandbox

import (
	"context"
	"io"
)

// Sandbox executes commands in isolated containers.
type Sandbox interface {
	Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error)
	PreflightCheck(ctx context.Context) error
}

// ExecuteRequest contains all parameters needed to run a command in a container.
type ExecuteRequest struct {
	ImageRef string
	Command  []string
	Mounts   []Mount
	Stdin    io.Reader
	Limits   ResourceLimits
}

// Mount describes a host path to be mounted into the container.
type Mount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// ResourceLimits defines execution constraints.
type ResourceLimits struct {
	CPUTimeMs   int
	WallTimeMs  int
	MemoryMB    int
	OutputBytes int64
}

// ExecuteResult contains the execution outcome and metrics.
type ExecuteResult struct {
	ExitCode  int
	Stdout    string
	Stderr    string
	CPUTimeMs int
	MemoryMB  int
	Verdict   Verdict
	ExtraInfo string
}

// Verdict represents the execution result classification.
type Verdict int

// Execution verdicts.
const (
	VerdictOK Verdict = iota
	VerdictTLE
	VerdictMLE
	VerdictOLE
	VerdictRE
)
