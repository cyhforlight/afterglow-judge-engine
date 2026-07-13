package resource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBundled(t *testing.T) {
	b, err := NewBundled()
	require.NoError(t, err)

	header, err := b.Get(context.Background(), "testlib.h")
	require.NoError(t, err)
	assert.NotEmpty(t, header)

	err = b.Stat(context.Background(), "checkers/default.cpp")
	require.NoError(t, err)
}
