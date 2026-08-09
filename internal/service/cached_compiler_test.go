package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"afterglow-judge-engine/internal/execution"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cachedCompilerFake struct {
	runStub
	mu     sync.Mutex
	result execution.CompileResult
	err    error
	calls  int
}

type runStub struct{}

func (runStub) Run(
	context.Context,
	execution.RunRequest,
) (execution.RunResult, error) {
	return execution.RunResult{}, nil
}

func (c *cachedCompilerFake) Compile(
	_ context.Context,
	_ execution.CompileRequest,
) (execution.CompileResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++
	return c.result, c.err
}

func testCompiledArtifact() *execution.Artifact {
	return &execution.Artifact{Name: "a.out", Data: []byte("binary"), Mode: 0o755}
}

func testCompileRequest(content string) execution.CompileRequest {
	return execution.CompileRequest{
		Files:        []execution.File{{Name: "main.cpp", Content: []byte(content), Mode: 0o644}},
		ImageRef:     "gcc:12",
		Command:      []string{"g++", "main.cpp"},
		ArtifactName: "a.out",
		Limits:       execution.Limits{WallTimeMs: 3000},
	}
}

func TestCachedCompiler_CacheHit(t *testing.T) {
	inner := &cachedCompilerFake{
		result: execution.CompileResult{Artifact: testCompiledArtifact()},
	}
	cc, err := newCachedCompiler(inner, 16)
	require.NoError(t, err)

	req := testCompileRequest("hello")
	out1, err := cc.Compile(t.Context(), req)
	require.NoError(t, err)
	assert.NotNil(t, out1.Artifact)

	out2, err := cc.Compile(t.Context(), req)
	require.NoError(t, err)
	assert.NotNil(t, out2.Artifact)
	assert.Equal(t, 1, inner.calls, "inner should only be called once")
}

func TestCachedCompiler_FailedCompileNotCached(t *testing.T) {
	inner := &cachedCompilerFake{
		result: execution.CompileResult{Log: "error"},
	}
	cc, err := newCachedCompiler(inner, 16)
	require.NoError(t, err)

	req := testCompileRequest("bad")
	_, err = cc.Compile(t.Context(), req)
	require.NoError(t, err)

	_, err = cc.Compile(t.Context(), req)
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
			result:       execution.CompileResult{Artifact: testCompiledArtifact()},
		}
		cc, err := newCachedCompiler(inner, 16)
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

func TestCachedCompiler_CallerCancellationDoesNotCancelSharedCompile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var compileCount atomic.Int32
		release := make(chan struct{})
		started := make(chan context.Context, 1)
		inner := &gatedCompiler{
			release:      release,
			started:      started,
			compileCount: &compileCount,
			result:       execution.CompileResult{Artifact: testCompiledArtifact()},
		}
		cc, err := newCachedCompiler(inner, 16)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		firstResult := make(chan error, 1)
		go func() {
			_, err := cc.Compile(ctx, testCompileRequest("shared"))
			firstResult <- err
		}()

		compileCtx := <-started
		secondResult := make(chan error, 1)
		go func() {
			_, err := cc.Compile(t.Context(), testCompileRequest("shared"))
			secondResult <- err
		}()
		synctest.Wait()

		cancel()
		synctest.Wait()
		require.ErrorIs(t, <-firstResult, context.Canceled)
		require.NoError(t, compileCtx.Err())

		close(release)
		synctest.Wait()
		require.NoError(t, <-secondResult)
		assert.Equal(t, int32(1), compileCount.Load())
	})
}

func TestCachedCompiler_ErrorNotCached(t *testing.T) {
	inner := &cachedCompilerFake{err: errors.New("infra error")}
	cc, err := newCachedCompiler(inner, 16)
	require.NoError(t, err)

	req := testCompileRequest("err")
	_, err = cc.Compile(t.Context(), req)
	require.Error(t, err)

	_, err = cc.Compile(t.Context(), req)
	require.Error(t, err)
	assert.Equal(t, 2, inner.calls)
}

// gatedCompiler blocks on a channel to test singleflight coalescing.
type gatedCompiler struct {
	runStub
	release      chan struct{}
	started      chan context.Context
	compileCount *atomic.Int32
	result       execution.CompileResult
}

func (g *gatedCompiler) Compile(
	ctx context.Context,
	_ execution.CompileRequest,
) (execution.CompileResult, error) {
	if g.started != nil {
		g.started <- ctx
	}
	<-g.release
	g.compileCount.Add(1)
	return g.result, nil
}
