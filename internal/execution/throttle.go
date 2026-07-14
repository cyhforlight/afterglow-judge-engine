package execution

import (
	"context"

	"golang.org/x/sync/semaphore"
)

type throttledExecutor struct {
	inner Executor
	sem   *semaphore.Weighted
}

// NewThrottledExecutor wraps inner with a shared concurrency semaphore.
func NewThrottledExecutor(inner Executor, sem *semaphore.Weighted) Executor {
	if sem == nil {
		panic("semaphore is required")
	}
	return &throttledExecutor{inner: inner, sem: sem}
}

func (e *throttledExecutor) Execute(ctx context.Context, job Job) (Result, error) {
	if err := e.sem.Acquire(ctx, 1); err != nil {
		return Result{}, err
	}
	defer e.sem.Release(1)

	return e.inner.Execute(ctx, job)
}
