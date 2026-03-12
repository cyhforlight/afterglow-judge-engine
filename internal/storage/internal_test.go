package storage

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalStorage_Get(t *testing.T) {
	storage := newInternalStorage(fstest.MapFS{
		"test-resource.txt": &fstest.MapFile{Data: []byte("test resource content")},
	})

	data, err := storage.Get(context.Background(), "test-resource.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("test resource content"), data)
}

func TestInternalStorage_Get_NestedPath(t *testing.T) {
	storage := newInternalStorage(fstest.MapFS{
		"checkers/ncmp.cpp": &fstest.MapFile{Data: []byte("int main() {}")},
	})

	data, err := storage.Get(context.Background(), "checkers/ncmp.cpp")
	require.NoError(t, err)
	assert.Equal(t, []byte("int main() {}"), data)
}

func TestInternalStorage_Get_NotFound(t *testing.T) {
	storage := newInternalStorage(fstest.MapFS{})

	_, err := storage.Get(context.Background(), "nonexistent.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestInternalStorage_Stat(t *testing.T) {
	storage := newInternalStorage(fstest.MapFS{
		"test-resource.txt": &fstest.MapFile{Data: []byte("test resource content")},
	})

	err := storage.Stat(context.Background(), "test-resource.txt")
	require.NoError(t, err)

	err = storage.Stat(context.Background(), "missing.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestInternalStorage_Get_RejectsInvalidKeys(t *testing.T) {
	storage := newInternalStorage(fstest.MapFS{
		"test.txt": &fstest.MapFile{Data: []byte("ok")},
	})

	tests := []struct {
		name  string
		key   string
		error string
	}{
		{name: "empty", key: "", error: "resource key is required"},
		{name: "absolute", key: "/etc/passwd", error: "resource key must be relative"},
		{name: "parent traversal", key: "../secret.txt", error: "escapes base directory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := storage.Get(context.Background(), tt.key)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.error)
		})
	}
}

func TestInternalStorage_Get_ReturnsCopy(t *testing.T) {
	storage := newInternalStorage(fstest.MapFS{
		"test.txt": &fstest.MapFile{Data: []byte("hello")},
	})

	firstRead, err := storage.Get(context.Background(), "test.txt")
	require.NoError(t, err)
	firstRead[0] = 'H'

	secondRead, err := storage.Get(context.Background(), "test.txt")
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), secondRead)
}

func TestNewBundledInternalStorage(t *testing.T) {
	storage, err := NewBundledInternalStorage()
	require.NoError(t, err)

	header, err := storage.Get(context.Background(), "testlib.h")
	require.NoError(t, err)
	assert.NotEmpty(t, header)

	err = storage.Stat(context.Background(), "checkers/default.cpp")
	require.NoError(t, err)
}
