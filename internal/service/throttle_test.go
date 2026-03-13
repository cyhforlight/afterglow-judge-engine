package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blockingRunner blocks until unblock is closed, tracking concurrent count.
type blockingRunner struct {
	unblock    chan struct{}
	concurrent atomic.Int32
	peak       atomic.Int32
}

func (r *blockingRunner) PreflightCheck(_ context.Context) error { return nil }

func (r *blockingRunner) Run(_ context.Context, _ RunRequest) (RunResult, error) {
	cur := r.concurrent.Add(1)
	for {
		old := r.peak.Load()
		if cur <= old || r.peak.CompareAndSwap(old, cur) {
			break
		}
	}
	<-r.unblock
	r.concurrent.Add(-1)
	return RunResult{}, nil
}

// blockingCompiler mirrors blockingRunner for the Compiler interface.
type blockingCompiler struct {
	unblock    chan struct{}
	concurrent atomic.Int32
	peak       atomic.Int32
}

func (c *blockingCompiler) Compile(_ context.Context, _ CompileRequest) (CompileOutput, error) {
	cur := c.concurrent.Add(1)
	for {
		old := c.peak.Load()
		if cur <= old || c.peak.CompareAndSwap(old, cur) {
			break
		}
	}
	<-c.unblock
	c.concurrent.Add(-1)
	return CompileOutput{}, nil
}

func TestThrottledRunner_ConcurrencyLimit(t *testing.T) {
	const limit = 2
	sem := make(chan struct{}, limit)
	inner := &blockingRunner{unblock: make(chan struct{})}
	throttled := NewThrottledRunner(inner, sem)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = throttled.Run(context.Background(), RunRequest{})
		}()
	}

	// Wait for the semaphore to fill up.
	time.Sleep(50 * time.Millisecond)
	assert.LessOrEqual(t, inner.peak.Load(), int32(limit))

	close(inner.unblock)
	wg.Wait()
}

func TestThrottledCompiler_ConcurrencyLimit(t *testing.T) {
	const limit = 2
	sem := make(chan struct{}, limit)
	inner := &blockingCompiler{unblock: make(chan struct{})}
	throttled := NewThrottledCompiler(inner, sem)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = throttled.Compile(context.Background(), CompileRequest{})
		}()
	}

	time.Sleep(50 * time.Millisecond)
	assert.LessOrEqual(t, inner.peak.Load(), int32(limit))

	close(inner.unblock)
	wg.Wait()
}

func TestThrottledRunner_ContextCancel(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // fill the semaphore
	throttled := NewThrottledRunner(&blockingRunner{unblock: make(chan struct{})}, sem)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := throttled.Run(ctx, RunRequest{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestThrottledCompiler_ContextCancel(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // fill the semaphore
	throttled := NewThrottledCompiler(&blockingCompiler{unblock: make(chan struct{})}, sem)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := throttled.Compile(ctx, CompileRequest{})
	require.ErrorIs(t, err, context.Canceled)
}

func TestThrottled_SharedSemaphore(t *testing.T) {
	const limit = 2
	sem := make(chan struct{}, limit)
	innerRunner := &blockingRunner{unblock: make(chan struct{})}
	innerCompiler := &blockingCompiler{unblock: make(chan struct{})}
	throttledR := NewThrottledRunner(innerRunner, sem)
	throttledC := NewThrottledCompiler(innerCompiler, sem)

	var wg sync.WaitGroup
	// Launch 3 runner + 3 compiler goroutines sharing the same sem of size 2.
	for range 3 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = throttledR.Run(context.Background(), RunRequest{})
		}()
		go func() {
			defer wg.Done()
			_, _ = throttledC.Compile(context.Background(), CompileRequest{})
		}()
	}

	time.Sleep(50 * time.Millisecond)
	totalPeak := innerRunner.peak.Load() + innerCompiler.peak.Load()
	assert.LessOrEqual(t, totalPeak, int32(limit))

	close(innerRunner.unblock)
	close(innerCompiler.unblock)
	wg.Wait()
}

func TestThrottledRunner_PreflightCheckBypassesSemaphore(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // fill the semaphore completely

	inner := &blockingRunner{unblock: make(chan struct{})}
	throttled := NewThrottledRunner(inner, sem)

	// PreflightCheck should NOT block even though sem is full.
	err := throttled.PreflightCheck(context.Background())
	assert.NoError(t, err)
}
