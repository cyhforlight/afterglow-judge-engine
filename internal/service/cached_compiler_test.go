package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCompileRequest(content string) CompileRequest {
	return CompileRequest{
		Files:        []workspace.File{{Name: "main.cpp", Content: []byte(content), Mode: 0o644}},
		ImageRef:     "gcc:12",
		Command:      []string{"g++", "main.cpp"},
		ArtifactName: "a.out",
	}
}

func TestCachedCompiler_CacheHit(t *testing.T) {
	inner := &fakeCompiler{
		result:   model.CompileResult{Succeeded: true},
		artifact: testCompiledArtifact(),
	}
	cc, err := NewCachedCompiler(inner, 16)
	require.NoError(t, err)

	req := testCompileRequest("hello")
	out1, err := cc.Compile(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, out1.Result.Succeeded)

	out2, err := cc.Compile(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, out2.Result.Succeeded)
	assert.Equal(t, 1, inner.calls, "inner should only be called once")
}

func TestCachedCompiler_FailedCompileNotCached(t *testing.T) {
	inner := &fakeCompiler{
		result: model.CompileResult{Succeeded: false, Log: "error"},
	}
	cc, err := NewCachedCompiler(inner, 16)
	require.NoError(t, err)

	req := testCompileRequest("bad")
	_, err = cc.Compile(context.Background(), req)
	require.NoError(t, err)

	_, err = cc.Compile(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 2, inner.calls, "failed compiles should not be cached")
}

func TestCachedCompiler_Singleflight(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var compileCount atomic.Int32
		release := make(chan struct{})
		inner := &gatedCompiler{
			release:      release,
			compileCount: &compileCount,
			result:       model.CompileResult{Succeeded: true},
			artifact:     testCompiledArtifact(),
		}
		cc, err := NewCachedCompiler(inner, 16)
		require.NoError(t, err)

		req := testCompileRequest("concurrent")
		const goroutines = 5
		errs := make([]error, goroutines)
		for i := range goroutines {
			go func() {
				_, errs[i] = cc.Compile(t.Context(), req)
			}()
		}

		synctest.Wait()
		close(release)
		synctest.Wait()

		for i, err := range errs {
			require.NoError(t, err, "goroutine %d", i)
		}
		assert.Equal(t, int32(1), compileCount.Load(), "singleflight should coalesce to 1 compile")
	})
}

func TestCachedCompiler_ErrorNotCached(t *testing.T) {
	var calls atomic.Int32
	inner := &countingErrCompiler{calls: &calls, err: errors.New("infra error")}
	cc, err := NewCachedCompiler(inner, 16)
	require.NoError(t, err)

	req := testCompileRequest("err")
	_, err = cc.Compile(context.Background(), req)
	require.Error(t, err)

	// Second call should also hit inner (error not cached).
	_, err = cc.Compile(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, int32(2), calls.Load())
}

// countingErrCompiler always returns an error and counts calls.
type countingErrCompiler struct {
	calls *atomic.Int32
	err   error
}

func (c *countingErrCompiler) Compile(_ context.Context, _ CompileRequest) (CompileOutput, error) {
	c.calls.Add(1)
	return CompileOutput{}, c.err
}

// gatedCompiler blocks on a channel to test singleflight coalescing.
type gatedCompiler struct {
	release      chan struct{}
	compileCount *atomic.Int32
	result       model.CompileResult
	artifact     *model.CompiledArtifact
}

func (g *gatedCompiler) Compile(_ context.Context, _ CompileRequest) (CompileOutput, error) {
	<-g.release
	g.compileCount.Add(1)
	return CompileOutput{Result: g.result, Artifact: g.artifact}, nil
}
