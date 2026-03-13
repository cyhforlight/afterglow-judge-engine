package service

import "context"

// throttledRunner decorates a Runner with a shared semaphore that limits
// the number of concurrent container operations (workspace create → cleanup).
type throttledRunner struct {
	inner Runner
	sem   chan struct{}
}

// NewThrottledRunner wraps inner with a concurrency-limiting semaphore.
// The sem channel is created and shared by the caller.
func NewThrottledRunner(inner Runner, sem chan struct{}) Runner {
	return &throttledRunner{inner: inner, sem: sem}
}

func (r *throttledRunner) PreflightCheck(ctx context.Context) error {
	return r.inner.PreflightCheck(ctx)
}

func (r *throttledRunner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	select {
	case r.sem <- struct{}{}:
		if ctx.Err() != nil {
			<-r.sem
			return RunResult{}, ctx.Err()
		}
		defer func() { <-r.sem }()
		return r.inner.Run(ctx, req)
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	}
}

// throttledCompiler decorates a Compiler with a shared semaphore.
type throttledCompiler struct {
	inner Compiler
	sem   chan struct{}
}

// NewThrottledCompiler wraps inner with a concurrency-limiting semaphore.
func NewThrottledCompiler(inner Compiler, sem chan struct{}) Compiler {
	return &throttledCompiler{inner: inner, sem: sem}
}

func (c *throttledCompiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	select {
	case c.sem <- struct{}{}:
		if ctx.Err() != nil {
			<-c.sem
			return CompileOutput{}, ctx.Err()
		}
		defer func() { <-c.sem }()
		return c.inner.Compile(ctx, req)
	case <-ctx.Done():
		return CompileOutput{}, ctx.Err()
	}
}
