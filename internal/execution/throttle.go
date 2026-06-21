package execution

import "context"

type throttledExecutor struct {
	inner Executor
	sem   chan struct{}
}

// NewThrottledExecutor wraps inner with a shared concurrency semaphore.
func NewThrottledExecutor(inner Executor, sem chan struct{}) Executor {
	if sem == nil {
		panic("semaphore channel is required: a nil channel blocks forever")
	}
	return &throttledExecutor{inner: inner, sem: sem}
}

func (e *throttledExecutor) PreflightCheck(ctx context.Context) error {
	return e.inner.PreflightCheck(ctx)
}

func (e *throttledExecutor) Execute(ctx context.Context, job Job) (Result, error) {
	select {
	case e.sem <- struct{}{}:
		if ctx.Err() != nil {
			<-e.sem
			return Result{}, ctx.Err()
		}
		defer func() { <-e.sem }()
		return e.inner.Execute(ctx, job)
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}
