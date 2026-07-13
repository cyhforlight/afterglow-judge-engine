package sandbox

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestCPUIdsFromAffinity(t *testing.T) {
	var affinity unix.CPUSet
	for _, cpuID := range []int{1, 4, 7} {
		affinity.Set(cpuID)
	}

	assert.Equal(t, []int{1, 4, 7}, cpuIDsFromAffinity(&affinity))
}

func TestNewCPUPoolFromIDs_RejectsEmptySet(t *testing.T) {
	_, err := newCPUPoolFromIDs(nil)
	require.EqualError(t, err, "cpu affinity contains no available CPUs")
}

func TestCPUPool_LeasesCPUExclusively(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pool, err := newCPUPoolFromIDs([]int{4})
		require.NoError(t, err)

		cpuID, err := pool.acquire(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 4, cpuID)

		type acquireResult struct {
			cpuID int
			err   error
		}
		acquired := make(chan acquireResult, 1)
		go func() {
			cpuID, err := pool.acquire(t.Context())
			acquired <- acquireResult{cpuID: cpuID, err: err}
		}()

		synctest.Wait()
		assert.Empty(t, acquired)

		pool.release(cpuID)
		synctest.Wait()
		result := <-acquired
		require.NoError(t, result.err)
		assert.Equal(t, 4, result.cpuID)
	})
}

func TestCPUPool_AcquireHonorsContextCancellation(t *testing.T) {
	pool, err := newCPUPoolFromIDs([]int{0})
	require.NoError(t, err)

	cpuID, err := pool.acquire(t.Context())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = pool.acquire(ctx)
	require.ErrorIs(t, err, context.Canceled)

	pool.release(cpuID)
	reacquiredCPU, err := pool.acquire(t.Context())
	require.NoError(t, err)
	assert.Equal(t, cpuID, reacquiredCPU)
}
