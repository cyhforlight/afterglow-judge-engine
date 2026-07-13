package resource

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBundled(t *testing.T) {
	b, err := NewBundled()
	require.NoError(t, err)

	header, err := fs.ReadFile(b, "testlib.h")
	require.NoError(t, err)
	assert.NotEmpty(t, header)

	_, err = fs.Stat(b, "checkers/default.cpp")
	require.NoError(t, err)
}
