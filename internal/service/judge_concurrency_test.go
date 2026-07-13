package service

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
)

type gatedRunner struct {
	result  RunResult
	release <-chan struct{}
	active  atomic.Int32
}

func (r *gatedRunner) PreflightCheck(context.Context) error {
	return nil
}

func (r *gatedRunner) Run(context.Context, RunRequest) (RunResult, error) {
	r.active.Add(1)
	defer r.active.Add(-1)

	if r.release != nil {
		<-r.release
	}
	return r.result, nil
}

// TestJudgeEngine_ConcurrencyLimit verifies that maxConcurrent limits parallel Judge() calls.
func TestJudgeEngine_ConcurrencyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const maxConcurrent = 2
		const numRequests = 5

		release := make(chan struct{})
		runner := &gatedRunner{
			release: release,
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
		req := baseJudgeRequest()
		results := make([]model.JudgeResult, numRequests)

		for i := range numRequests {
			go func() {
				results[i] = engine.Judge(t.Context(), req)
			}()
		}

		synctest.Wait()
		assert.Equal(t, int32(maxConcurrent), runner.active.Load())

		close(release)
		synctest.Wait()
		for _, result := range results {
			assert.Equal(t, model.JudgeStatusOK, result.Status)
		}
	})
}

// TestJudgeEngine_ConcurrencyTimeout verifies context cancellation while waiting for capacity.
func TestJudgeEngine_ConcurrencyTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		runner := &gatedRunner{
			release: release,
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

		go engine.Judge(t.Context(), req)
		synctest.Wait()
		assert.Equal(t, int32(1), runner.active.Load())

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		result := engine.Judge(ctx, req)
		assert.Equal(t, model.JudgeStatusSystemError, result.Status)
		assert.Contains(t, result.Compile.Log, "timed out while waiting for capacity")

		close(release)
	})
}

// TestJudgeEngine_ConcurrencyRaceCondition verifies that canceled requests don't occupy slots.
// This test specifically addresses the race condition where ctx.Done() and semaphore
// become ready simultaneously, and select might choose the semaphore case.
func TestJudgeEngine_ConcurrencyRaceCondition(t *testing.T) {
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

	for range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		result := engine.Judge(ctx, req)
		assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	}

	assert.Zero(t, runner.calls)
}
