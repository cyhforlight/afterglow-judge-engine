package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLimitedWriter_SharedBudgetAndOverflowSignal(t *testing.T) {
	limiter := newOutputLimiter(5)
	stdout := newLimitedWriter(limiter)
	stderr := newLimitedWriter(limiter)

	n, err := stdout.Write([]byte("abc"))
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	n, err = stderr.Write([]byte("defg"))
	require.NoError(t, err)
	assert.Equal(t, 4, n, "writer must report consumed bytes so container IO does not retry forever")

	assert.Equal(t, "abc", stdout.String())
	assert.Equal(t, "de", stderr.String())
	assert.False(t, stdout.isOverflowed())
	assert.True(t, stderr.isOverflowed())

	select {
	case <-limiter.ch:
	default:
		t.Fatal("overflow should signal the execution watcher")
	}

	n, err = stderr.Write([]byte("more"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "de", stderr.String(), "overflowed writer must not grow unbounded")
}

func TestLimitedWriter_ExactLimitDoesNotOverflow(t *testing.T) {
	limiter := newOutputLimiter(5)
	stdout := newLimitedWriter(limiter)

	_, err := stdout.Write([]byte("12345"))
	require.NoError(t, err)

	assert.Equal(t, "12345", stdout.String())
	assert.False(t, stdout.isOverflowed())
}
