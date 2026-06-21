package resource

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBundled_Get_NestedPath(t *testing.T) {
	b := newBundled(fstest.MapFS{
		"checkers/ncmp.cpp": &fstest.MapFile{Data: []byte("int main() {}")},
	})

	data, err := b.Get(context.Background(), "checkers/ncmp.cpp")
	require.NoError(t, err)
	assert.Equal(t, []byte("int main() {}"), data)
}

func TestBundled_Get_NotFound(t *testing.T) {
	b := newBundled(fstest.MapFS{})

	_, err := b.Get(context.Background(), "nonexistent.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestBundled_Stat(t *testing.T) {
	b := newBundled(fstest.MapFS{
		"test-resource.txt": &fstest.MapFile{Data: []byte("test resource content")},
	})

	err := b.Stat(context.Background(), "test-resource.txt")
	require.NoError(t, err)

	err = b.Stat(context.Background(), "missing.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

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
