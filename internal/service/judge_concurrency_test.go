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

type gatedProgram struct {
	result  RunResult
	release <-chan struct{}
	active  atomic.Int32
	calls   atomic.Int32
}

func (p *gatedProgram) Run(context.Context, string, int, int) (RunResult, error) {
	p.calls.Add(1)
	p.active.Add(1)
	defer p.active.Add(-1)

	if p.release != nil {
		<-p.release
	}
	return p.result, nil
}

// TestJudgeEngine_ConcurrencyLimit verifies that maxConcurrent limits parallel Judge() calls.
func TestJudgeEngine_ConcurrencyLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const maxConcurrent = 2
		const numRequests = 5

		release := make(chan struct{})
		program := &gatedProgram{
			release: release,
			result: RunResult{
				Verdict:   execution.VerdictOK,
				Stdout:    "output",
				CPUTimeMs: 10,
				MemoryMB:  10,
			},
		}

		engine := newJudgeEngine(
			newFakeLanguageWithProgram(program),
			newFakeChecker(),
			nil,
			maxConcurrent,
			model.DefaultJudgeLimits(),
		)
		req := baseJudgeRequest()
		results := make([]model.JudgeResult, numRequests)

		for i := range numRequests {
			go func() {
				results[i] = engine.Judge(t.Context(), req)
			}()
		}

		synctest.Wait()
		assert.Equal(t, int32(maxConcurrent), program.active.Load())

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
		program := &gatedProgram{
			release: release,
			result: RunResult{
				Verdict:   execution.VerdictOK,
				Stdout:    "output",
				CPUTimeMs: 10,
				MemoryMB:  10,
			},
		}

		engine := newJudgeEngine(
			newFakeLanguageWithProgram(program),
			newFakeChecker(),
			nil,
			1,
			model.DefaultJudgeLimits(),
		)
		req := baseJudgeRequest()

		go engine.Judge(t.Context(), req)
		synctest.Wait()
		assert.Equal(t, int32(1), program.active.Load())

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		result := engine.Judge(ctx, req)
		assert.Equal(t, model.JudgeStatusSystemError, result.Status)
		assert.Contains(t, result.Compile.Log, "timed out while waiting for capacity")

		close(release)
	})
}

// TestJudgeEngine_CanceledContextDoesNotAcquireCapacity verifies that canceled requests don't occupy slots.
func TestJudgeEngine_CanceledContextDoesNotAcquireCapacity(t *testing.T) {
	program := &gatedProgram{
		result: RunResult{
			Verdict:   execution.VerdictOK,
			Stdout:    "output",
			CPUTimeMs: 10,
			MemoryMB:  10,
		},
	}

	engine := newJudgeEngine(
		newFakeLanguageWithProgram(program),
		newFakeChecker(),
		nil,
		1,
		model.DefaultJudgeLimits(),
	)

	req := baseJudgeRequest()

	for range 20 {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		result := engine.Judge(ctx, req)
		assert.Equal(t, model.JudgeStatusSystemError, result.Status)
	}

	assert.Zero(t, program.calls.Load())
}
