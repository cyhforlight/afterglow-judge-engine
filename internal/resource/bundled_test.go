package resource

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundled_Get_ReturnsCopy(t *testing.T) {
	b := newBundled(fstest.MapFS{
		"test.txt": &fstest.MapFile{Data: []byte("hello")},
	})

	firstRead, err := b.Get(context.Background(), "test.txt")
	require.NoError(t, err)
	firstRead[0] = 'H'

	secondRead, err := b.Get(context.Background(), "test.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), secondRead)
}

func TestNewBundled(t *testing.T) {
	b, err := NewBundled()
	require.NoError(t, err)

	header, err := b.Get(context.Background(), "testlib.h")
	require.NoError(t, err)
	assert.NotEmpty(t, header)

	err = b.Stat(context.Background(), "checkers/default.cpp")
	require.NoError(t, err)
}
