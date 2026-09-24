// Package execution compiles and runs programs in temporary container workspaces.
package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"afterglow-judge-engine/internal/model"
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

// CompileRequest describes one isolated compilation.
type CompileRequest struct {
	Files        []File
	ImageRef     string
	Command      []string
	ArtifactName string
	Limits       Limits
}

// CompileResult contains compiler diagnostics, execution failure reasons, and
// the optional compiled artifact.
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

// RunResult contains the outcome of a sandboxed execution.
type RunResult struct {
	Verdict   model.Verdict
	ExitCode  int
	Stdout    string
	Stderr    string
	CPUTimeMs int
	MemoryMB  int
	ExtraInfo string
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

	// maxCompileArtifactBytes caps a compiled file before it enters host memory.
	maxCompileArtifactBytes = 64 * 1024 * 1024 // 64MiB
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
	RunResult
	compile CompileResult
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

	diagnostics := make([]string, 0, 3)
	for _, message := range []string{result.Stdout, result.Stderr, result.compile.Log} {
		if message != "" {
			diagnostics = append(diagnostics, message)
		}
	}
	switch result.Verdict {
	case model.VerdictTLE, model.VerdictMLE, model.VerdictOLE:
		diagnostics = append(diagnostics, result.ExtraInfo)
	}
	log := strings.Join(diagnostics, "\n")
	if result.Verdict == model.VerdictRE && strings.TrimSpace(log) == "" {
		log = fmt.Sprintf("compiler exited with code %d", result.ExitCode)
	}

	return CompileResult{Log: log, Artifact: result.compile.Artifact}, nil
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
	return result.RunResult, nil
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

	result = taskResult{RunResult: sandboxResultToRunResult(sandboxResult)}

	if t.artifactName == "" || result.ExitCode != 0 || result.Verdict != model.VerdictOK {
		return result, nil
	}

	result.compile, err = collectArtifact(ws, t.artifactName)
	if err != nil {
		return taskResult{}, err
	}
	return result, nil
}

// sandboxResultToRunResult translates the sandbox-internal verdict enum to the
// model-level verdict used by all layers above execution.
func sandboxResultToRunResult(r sandbox.ExecuteResult) RunResult {
	return RunResult{
		Verdict:   sandboxVerdictToModel(r.Verdict),
		ExitCode:  r.ExitCode,
		Stdout:    r.Stdout,
		Stderr:    r.Stderr,
		CPUTimeMs: r.CPUTimeMs,
		MemoryMB:  r.MemoryMB,
		ExtraInfo: r.ExtraInfo,
	}
}

func sandboxVerdictToModel(v sandbox.Verdict) model.Verdict {
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

func collectArtifact(ws *workspace, name string) (CompileResult, error) {
	info, err := ws.stat(name)
	if err != nil {
		return CompileResult{}, fmt.Errorf("stat artifact %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return CompileResult{Log: fmt.Sprintf("compiled artifact %q is not a regular file", name)}, nil
	}
	if info.Size() > maxCompileArtifactBytes {
		return CompileResult{Log: fmt.Sprintf(
			"compiled artifact %q exceeds size limit (%d bytes > %d bytes)",
			name, info.Size(), maxCompileArtifactBytes,
		)}, nil
	}

	data, err := ws.readFile(name)
	if err != nil {
		return CompileResult{}, fmt.Errorf("read artifact %q: %w", name, err)
	}

	return CompileResult{Artifact: &Artifact{Name: name, Data: data, Mode: info.Mode().Perm()}}, nil
}
