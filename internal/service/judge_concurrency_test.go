package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
)

// slowRunner wraps a runner and adds artificial delay to track concurrency.
type slowRunner struct {
	inner        Runner
	activeJudges *atomic.Int32
	maxObserved  *atomic.Int32
	delayMs      int
}

func (r *slowRunner) PreflightCheck(ctx context.Context) error {
	return r.inner.PreflightCheck(ctx)
}

func (r *slowRunner) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	current := r.activeJudges.Add(1)
	defer r.activeJudges.Add(-1)

	// Track max concurrent judges
	for {
		observed := r.maxObserved.Load()
		if current <= observed || r.maxObserved.CompareAndSwap(observed, current) {
			break
		}
	}

	// Simulate slow execution
	time.Sleep(time.Duration(r.delayMs) * time.Millisecond)

	return r.inner.Run(ctx, req)
}

// TestJudgeEngine_ConcurrencyLimit verifies that maxConcurrent limits parallel Judge() calls.
func TestJudgeEngine_ConcurrencyLimit(t *testing.T) {
	var activeJudges atomic.Int32
	var maxObserved atomic.Int32

	baseRunner := &fakeRunner{
		runResult: RunResult{
			Verdict:   execution.VerdictOK,
			Stdout:    "output",
			CPUTimeMs: 10,
			MemoryMB:  10,
		},
	}

	slowRunnerWrapper := &slowRunner{
		inner:        baseRunner,
		activeJudges: &activeJudges,
		maxObserved:  &maxObserved,
		delayMs:      50,
	}

	compiler := &fakeCompiler{compileResults: successCompileResults()}
	resources := &fakeResourceStore{files: map[string][]byte{
		"checkers/default.cpp": []byte("checker"),
		testlibHeaderKey:       []byte("header"),
	}}

	maxConcurrent := 2
	engine := NewJudgeEngine(compiler, compiler, slowRunnerWrapper, resources, nil, maxConcurrent, model.DefaultJudgeLimits())

	ctx := context.Background()
	req := baseJudgeRequest()

	// Launch 5 concurrent Judge() calls
	numRequests := 5
	var wg sync.WaitGroup
	for range numRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := engine.Judge(ctx, req)
			assert.Equal(t, model.JudgeStatusOK, result.Status)
		}()
	}

	wg.Wait()

	// Verify that at most maxConcurrent judges ran in parallel
	assert.LessOrEqual(t, maxObserved.Load(), int32(maxConcurrent),
		"observed %d concurrent judges, but limit is %d", maxObserved.Load(), maxConcurrent)
}

// TestJudgeEngine_ConcurrencyTimeout verifies context cancellation while waiting for capacity.
func TestJudgeEngine_ConcurrencyTimeout(t *testing.T) {
	blockingRunner := &fakeRunner{
		runResult: RunResult{
			Verdict:   execution.VerdictOK,
			Stdout:    "output",
			CPUTimeMs: 10,
			MemoryMB:  10,
		},
	}

	// Wrap to add delay
	slowRunnerWrapper := &slowRunner{
		inner:        blockingRunner,
		activeJudges: &atomic.Int32{},
		maxObserved:  &atomic.Int32{},
		delayMs:      5000, // 5 seconds
	}

	compiler := &fakeCompiler{compileResults: successCompileResults()}
	resources := &fakeResourceStore{files: map[string][]byte{
		"checkers/default.cpp": []byte("checker"),
		testlibHeaderKey:       []byte("header"),
	}}

	engine := NewJudgeEngine(compiler, compiler, slowRunnerWrapper, resources, nil, 1, model.DefaultJudgeLimits())

	req := baseJudgeRequest()

	// First request occupies the slot
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx := context.Background()
		engine.Judge(ctx, req)
	}()

	// Give first request time to acquire the semaphore
	time.Sleep(10 * time.Millisecond)

	// Second request with short timeout should fail
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := engine.Judge(ctx, req)
	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.Contains(t, result.Compile.Log, "timed out while waiting for capacity")

	// Wait for first request to complete
	wg.Wait()
}

// TestJudgeEngine_ConcurrencyRaceCondition verifies that canceled requests don't occupy slots.
// This test specifically addresses the race condition where ctx.Done() and semaphore
// become ready simultaneously, and select might choose the semaphore case.
func TestJudgeEngine_ConcurrencyRaceCondition(t *testing.T) {
	var executedCount atomic.Int32

	trackingRunner := &fakeRunner{
		runResult: RunResult{
			Verdict:   execution.VerdictOK,
			Stdout:    "output",
			CPUTimeMs: 10,
			MemoryMB:  10,
		},
	}

	// Wrap runner to track actual executions
	countingRunner := &slowRunner{
		inner:        trackingRunner,
		activeJudges: &atomic.Int32{},
		maxObserved:  &atomic.Int32{},
		delayMs:      100, // 100ms per execution
	}

	compiler := &fakeCompiler{compileResults: successCompileResults()}
	resources := &fakeResourceStore{files: map[string][]byte{
		"checkers/default.cpp": []byte("checker"),
		testlibHeaderKey:       []byte("header"),
	}}

	engine := NewJudgeEngine(compiler, compiler, countingRunner, resources, nil, 1, model.DefaultJudgeLimits())

	req := baseJudgeRequest()

	// Launch many requests with pre-canceled contexts
	// If the race condition exists, some will still execute
	numRequests := 20
	var wg sync.WaitGroup

	for range numRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Create already-canceled context
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // Cancel immediately

			result := engine.Judge(ctx, req)

			// If the fix works, all should return SystemError without executing
			if result.Status == model.JudgeStatusOK {
				executedCount.Add(1)
			}
		}()
	}

	wg.Wait()

	// With the fix, no canceled requests should execute
	// Without the fix, some would randomly execute due to select race
	executed := executedCount.Load()
	assert.Equal(t, int32(0), executed,
		"Expected 0 canceled requests to execute, but %d executed (race condition detected)", executed)
}
