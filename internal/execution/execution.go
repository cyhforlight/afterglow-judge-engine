// Package execution compiles and runs programs in temporary container workspaces.
package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"afterglow-judge-engine/internal/sandbox"

	"golang.org/x/sync/semaphore"
)

// Artifact is a file produced by a compilation.
type Artifact struct {
	Name string
	Data []byte
	Mode os.FileMode
}

// File describes a file available to a compilation or run.
type File struct {
	Name    string
	Content []byte
	Mode    os.FileMode
}

// Limits defines resource constraints for a compilation or run.
type Limits = sandbox.ResourceLimits

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

// CompileRequest describes one isolated compilation.
type CompileRequest struct {
	Files        []File
	ImageRef     string
	Command      []string
	ArtifactName string
	Limits       Limits
}

// CompileResult contains compiler diagnostics and the optional compiled artifact.
// A nil artifact means compilation finished without a usable output.
type CompileResult struct {
	Log      string
	Artifact *Artifact
}

// RunRequest describes one isolated program execution.
type RunRequest struct {
	Artifact Artifact
	Files    []File
	ImageRef string
	Command  []string
	Stdin    io.Reader
	Limits   Limits
}

// RunResult contains the outcome reported by the sandbox.
type RunResult = sandbox.ExecuteResult

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

// Executor compiles and runs programs with shared container capacity.
type Executor interface {
	Compile(ctx context.Context, req CompileRequest) (CompileResult, error)
	Run(ctx context.Context, req RunRequest) (RunResult, error)
}

type sandboxExecutor interface {
	Execute(ctx context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error)
}

type executor struct {
	sandbox sandboxExecutor
	sem     *semaphore.Weighted
}

type task struct {
	files         []File
	imageRef      string
	command       []string
	mountPath     string
	readOnlyMount bool
	stdin         io.Reader
	limits        Limits
	enableSeccomp bool
	artifactName  string
}

type taskResult struct {
	sandbox.ExecuteResult
	artifact *Artifact
}

// NewExecutor creates a capacity-limited executor backed by a sandbox.
func NewExecutor(sb sandboxExecutor, maxConcurrent int) (Executor, error) {
	if maxConcurrent <= 0 {
		return nil, fmt.Errorf("max concurrent executions must be positive, got %d", maxConcurrent)
	}
	return &executor{
		sandbox: sb,
		sem:     semaphore.NewWeighted(int64(maxConcurrent)),
	}, nil
}

// Compile runs a compiler and collects its single declared artifact on success.
func (e *executor) Compile(ctx context.Context, req CompileRequest) (CompileResult, error) {
	result, err := e.execute(ctx, task{
		files:         req.Files,
		imageRef:      req.ImageRef,
		command:       req.Command,
		mountPath:     "/work",
		readOnlyMount: false,
		limits:        req.Limits,
		enableSeccomp: false,
		artifactName:  req.ArtifactName,
	})
	if err != nil {
		return CompileResult{}, err
	}

	log := result.Stdout
	if result.Stderr != "" {
		if log != "" {
			log += "\n"
		}
		log += result.Stderr
	}

	return CompileResult{Log: log, Artifact: result.artifact}, nil
}

// Run executes a compiled artifact in a read-only, seccomp-restricted workspace.
func (e *executor) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	files := make([]File, 1, len(req.Files)+1)
	files[0] = File{Name: req.Artifact.Name, Content: req.Artifact.Data, Mode: req.Artifact.Mode}
	files = append(files, req.Files...)

	result, err := e.execute(ctx, task{
		files:         files,
		imageRef:      req.ImageRef,
		command:       req.Command,
		mountPath:     "/sandbox",
		readOnlyMount: true,
		stdin:         req.Stdin,
		limits:        req.Limits,
		enableSeccomp: true,
	})
	if err != nil {
		return RunResult{}, err
	}
	return result.ExecuteResult, nil
}

func (e *executor) execute(ctx context.Context, t task) (result taskResult, err error) {
	if err := e.sem.Acquire(ctx, 1); err != nil {
		return taskResult{}, err
	}
	defer e.sem.Release(1)

	ws, err := newWorkspace()
	if err != nil {
		return taskResult{}, fmt.Errorf("create workspace: %w", err)
	}
	defer func() {
		err = errors.Join(err, ws.cleanup())
	}()

	if err := ws.writeFiles(t.files); err != nil {
		return taskResult{}, fmt.Errorf("write execution files: %w", err)
	}

	sandboxReq := sandbox.ExecuteRequest{
		ImageRef: t.imageRef,
		Command:  t.command,
		MountDir: &sandbox.Mount{
			HostPath:      ws.dir(),
			ContainerPath: t.mountPath,
			ReadOnly:      t.readOnlyMount,
		},
		Stdin:         t.stdin,
		Limits:        t.limits,
		EnableSeccomp: t.enableSeccomp,
	}

	sandboxResult, err := e.sandbox.Execute(ctx, sandboxReq)
	if err != nil {
		return taskResult{}, fmt.Errorf("sandbox execute: %w", err)
	}
	if sandboxResult.Verdict == sandbox.VerdictUnknown {
		return taskResult{}, errors.New("sandbox execute returned unknown verdict")
	}

	result = taskResult{ExecuteResult: sandboxResult}

	if t.artifactName == "" || result.ExitCode != 0 || result.Verdict != VerdictOK {
		return result, nil
	}

	artifact, err := collectArtifact(ws, t.artifactName)
	if err != nil {
		return taskResult{}, err
	}
	result.artifact = artifact
	return result, nil
}

func collectArtifact(ws *workspace, name string) (*Artifact, error) {
	info, err := ws.stat(name)
	if err != nil {
		return nil, fmt.Errorf("stat artifact %q: %w", name, err)
	}

	data, err := ws.readFile(name)
	if err != nil {
		return nil, fmt.Errorf("read artifact %q: %w", name, err)
	}

	return &Artifact{Name: name, Data: data, Mode: info.Mode().Perm()}, nil
}
