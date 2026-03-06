package concurrency

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecutionLimiter_WithLimit(t *testing.T) {
	limiter := NewExecutionLimiter(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	readyCh := make(chan struct{}, 5)
	startedCh := make(chan struct{}, 5)
	releaseCh := make(chan struct{})
	errCh := make(chan error, 5)

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLimitedWorker(ctx, limiter, &concurrent, &maxConcurrent, readyCh, startedCh, releaseCh, errCh)
		}()
	}

	for range 5 {
		waitForSignal(ctx, t, readyCh, "workers to become ready")
	}
	for range 2 {
		waitForSignal(ctx, t, startedCh, "workers to acquire execution slots")
	}

	assert.Equal(t, int32(2), concurrent.Load())
	assert.Equal(t, int32(2), maxConcurrent.Load())

	select {
	case <-startedCh:
		t.Fatal("worker acquired an execution slot before one was released")
	default:
	}

	close(releaseCh)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	assert.Zero(t, concurrent.Load())
	assert.Equal(t, int32(2), maxConcurrent.Load())
}

func TestExecutionLimiter_ContextCancellation(t *testing.T) {
	limiter := NewExecutionLimiter(1)

	// Occupy the slot
	ctx1 := context.Background()
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		_ = limiter.WithLimit(ctx1, func() error {
			close(started)
			<-done
			return nil
		})
	}()

	<-started

	// Try to acquire with cancelled context
	ctx2, cancel := context.WithCancel(context.Background())
	cancel()

	err := limiter.WithLimit(ctx2, func() error {
		return nil
	})

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)

	close(done)
}

func TestExecutionLimiter_FunctionError(t *testing.T) {
	limiter := NewExecutionLimiter(1)
	ctx := context.Background()

	expectedErr := errors.New("test error")
	err := limiter.WithLimit(ctx, func() error {
		return expectedErr
	})

	assert.Equal(t, expectedErr, err)
}

func TestNewExecutionLimiter_InvalidLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int64
	}{
		{"zero limit", 0},
		{"negative limit", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewExecutionLimiter(tt.limit)
			assert.NotNil(t, limiter)

			// Should still work (defaults to 1)
			ctx := context.Background()
			err := limiter.WithLimit(ctx, func() error {
				return nil
			})
			assert.NoError(t, err)
		})
	}
}

func runLimitedWorker(
	ctx context.Context,
	limiter *ExecutionLimiter,
	concurrent *atomic.Int32,
	maxConcurrent *atomic.Int32,
	readyCh chan<- struct{},
	startedCh chan<- struct{},
	releaseCh <-chan struct{},
	errCh chan<- error,
) {
	readyCh <- struct{}{}

	errCh <- limiter.WithLimit(ctx, func() error {
		current := concurrent.Add(1)
		defer concurrent.Add(-1)

		trackMaxConcurrent(current, maxConcurrent)
		startedCh <- struct{}{}
		<-releaseCh

		return nil
	})
}

func trackMaxConcurrent(current int32, maxConcurrent *atomic.Int32) {
	for {
		maxVal := maxConcurrent.Load()
		if current <= maxVal || maxConcurrent.CompareAndSwap(maxVal, current) {
			return
		}
	}
}

func waitForSignal(ctx context.Context, t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatalf("timed out waiting for %s: %v", description, ctx.Err())
	}
}
