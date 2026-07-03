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

type gatedRunner struct {
	result  RunResult
	release <-chan struct{}
	started chan<- struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (r *gatedRunner) PreflightCheck(context.Context) error {
	return nil
}

func (r *gatedRunner) Run(context.Context, RunRequest) (RunResult, error) {
	current := r.active.Add(1)
	defer r.active.Add(-1)

	for {
		peak := r.peak.Load()
		if current <= peak || r.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	if r.started != nil {
		r.started <- struct{}{}
	}
	if r.release != nil {
		<-r.release
	}
	return r.result, nil
}

func newRunGate(t *testing.T) (<-chan struct{}, func()) {
	t.Helper()

	release := make(chan struct{})
	var once sync.Once
	closeGate := func() {
		once.Do(func() {
			close(release)
		})
	}
	t.Cleanup(closeGate)
	return release, closeGate
}

func waitForRunStarts(t *testing.T, started <-chan struct{}, count int) {
	t.Helper()

	for range count {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %d runner starts", count)
		}
	}
}

// TestJudgeEngine_ConcurrencyLimit verifies that maxConcurrent limits parallel Judge() calls.
func TestJudgeEngine_ConcurrencyLimit(t *testing.T) {
	const maxConcurrent = 2
	const numRequests = 5

	started := make(chan struct{}, 16)
	release, closeGate := newRunGate(t)
	runner := &gatedRunner{
		release: release,
		started: started,
		result: RunResult{
			Verdict:   execution.VerdictOK,
			Stdout:    "output",
			CPUTimeMs: 10,
			MemoryMB:  10,
		},
	}

	compiler := &fakeCompiler{compileResults: successCompileResults()}
	resources := &fakeResourceStore{files: map[string][]byte{
		"checkers/default.cpp": []byte("checker"),
		testlibHeaderKey:       []byte("header"),
	}}

	engine := NewJudgeEngine(compiler, compiler, runner, resources, nil, maxConcurrent, model.DefaultJudgeLimits())

	ctx := context.Background()
	req := baseJudgeRequest()

	var wg sync.WaitGroup
	for range numRequests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := engine.Judge(ctx, req)
			assert.Equal(t, model.JudgeStatusOK, result.Status)
		}()
	}

	waitForRunStarts(t, started, maxConcurrent)
	closeGate()
	wg.Wait()

	assert.LessOrEqual(t, runner.peak.Load(), int32(maxConcurrent),
		"observed %d concurrent judges, but limit is %d", runner.peak.Load(), maxConcurrent)
}

// TestJudgeEngine_ConcurrencyTimeout verifies context cancellation while waiting for capacity.
func TestJudgeEngine_ConcurrencyTimeout(t *testing.T) {
	started := make(chan struct{}, 4)
	release, closeGate := newRunGate(t)
	runner := &gatedRunner{
		release: release,
		started: started,
		result: RunResult{
			Verdict:   execution.VerdictOK,
			Stdout:    "output",
			CPUTimeMs: 10,
			MemoryMB:  10,
		},
	}

	compiler := &fakeCompiler{compileResults: successCompileResults()}
	resources := &fakeResourceStore{files: map[string][]byte{
		"checkers/default.cpp": []byte("checker"),
		testlibHeaderKey:       []byte("header"),
	}}

	engine := NewJudgeEngine(compiler, compiler, runner, resources, nil, 1, model.DefaultJudgeLimits())

	req := baseJudgeRequest()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx := context.Background()
		engine.Judge(ctx, req)
	}()

	waitForRunStarts(t, started, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result := engine.Judge(ctx, req)
	assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	assert.Contains(t, result.Compile.Log, "timed out while waiting for capacity")

	closeGate()
	wg.Wait()
}

// TestJudgeEngine_ConcurrencyRaceCondition verifies that canceled requests don't occupy slots.
// This test specifically addresses the race condition where ctx.Done() and semaphore
// become ready simultaneously, and select might choose the semaphore case.
func TestJudgeEngine_ConcurrencyRaceCondition(t *testing.T) {
	var executedCount atomic.Int32

	runner := &fakeRunner{
		runResult: RunResult{
			Verdict:   execution.VerdictOK,
			Stdout:    "output",
			CPUTimeMs: 10,
			MemoryMB:  10,
		},
	}

	compiler := &fakeCompiler{compileResults: successCompileResults()}
	resources := &fakeResourceStore{files: map[string][]byte{
		"checkers/default.cpp": []byte("checker"),
		testlibHeaderKey:       []byte("header"),
	}}

	engine := NewJudgeEngine(compiler, compiler, runner, resources, nil, 1, model.DefaultJudgeLimits())

	req := baseJudgeRequest()

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
			if result.Status == model.JudgeStatusOK {
				executedCount.Add(1)
			}
		}()
	}

	wg.Wait()

	executed := executedCount.Load()
	assert.Equal(t, int32(0), executed,
		"Expected 0 canceled requests to execute, but %d executed (race condition detected)", executed)
}
