package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"
)

const checkerCacheEntries = 64

// checkerCompiler uses a fixed profile and testlib snapshot to prepare cached checkers.
type checkerCompiler struct {
	executor      execution.Executor
	profile       compileConfig
	testlibHeader []byte
	cache         *lru.Cache[[sha256.Size]byte, execution.CompileResult]
	group         singleflight.Group
	ctx           context.Context
	cancel        context.CancelFunc
	lifecycleMu   sync.Mutex
	compilations  sync.WaitGroup
}

func newCheckerCompiler(executor execution.Executor, testlibHeader []byte) (*checkerCompiler, error) {
	cache, err := lru.New[[sha256.Size]byte, execution.CompileResult](checkerCacheEntries)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &checkerCompiler{
		executor:      executor,
		profile:       checkerCompileProfile(),
		testlibHeader: testlibHeader,
		cache:         cache,
		ctx:           ctx,
		cancel:        cancel,
	}, nil
}

func (c *checkerCompiler) close() {
	c.lifecycleMu.Lock()
	c.cancel()
	c.lifecycleMu.Unlock()
	c.compilations.Wait()
}

func (c *checkerCompiler) prepare(
	ctx context.Context,
	source []byte,
) (preparedChecker, model.CompileResult, error) {
	compilation, err := c.compile(ctx, source)
	if err != nil {
		return nil, model.CompileResult{}, err
	}

	result := model.CompileResult{
		Succeeded: compilation.Artifact != nil,
		Log:       compilation.Log,
	}
	if compilation.Artifact == nil {
		return nil, result, nil
	}
	return &compiledChecker{executor: c.executor, artifact: *compilation.Artifact}, result, nil
}

func (c *checkerCompiler) compile(ctx context.Context, source []byte) (execution.CompileResult, error) {
	// The profile and testlib snapshot are fixed for the lifetime of this cache,
	// so the checker source is the only varying compilation input.
	key := sha256.Sum256(source)
	if compilation, ok := c.cache.Get(key); ok {
		slog.DebugContext(ctx, "checker compile cache hit", "key", hex.EncodeToString(key[:8]))
		return compilation, nil
	}

	resultCh := c.group.DoChan(string(key[:]), func() (any, error) {
		// Admission and cancellation share a lock: Close must also cover a
		// singleflight callback that has been queued but has not started yet.
		c.lifecycleMu.Lock()
		if err := c.ctx.Err(); err != nil {
			c.lifecycleMu.Unlock()
			return nil, fmt.Errorf("checker setup failed: %w", err)
		}
		c.compilations.Add(1)
		c.lifecycleMu.Unlock()
		defer c.compilations.Done()

		compileCtx, cancel := context.WithTimeout(
			c.ctx,
			time.Duration(c.profile.TimeoutMs*execution.WallTimeMultiplier)*time.Millisecond,
		)
		defer cancel()

		if compilation, ok := c.cache.Get(key); ok {
			slog.DebugContext(
				compileCtx,
				"checker compile cache hit after singleflight wait",
				"key",
				hex.EncodeToString(key[:8]),
			)
			return compilation, nil
		}

		compilation, err := c.compileUncached(compileCtx, source)
		if err != nil {
			return nil, err
		}
		if compilation.Artifact != nil {
			c.cache.Add(key, compilation)
		}
		return compilation, nil
	})

	select {
	case <-ctx.Done():
		return execution.CompileResult{}, fmt.Errorf("checker setup failed: %w", ctx.Err())
	case result := <-resultCh:
		if result.Err != nil {
			return execution.CompileResult{}, result.Err
		}
		return result.Val.(execution.CompileResult), nil
	}
}

func (c *checkerCompiler) compileUncached(ctx context.Context, source []byte) (execution.CompileResult, error) {
	profile := c.profile
	compileOut, err := c.executor.Compile(ctx, execution.CompileRequest{
		Files: []execution.File{
			{Name: profile.SourceFile, Content: source, Mode: 0o644},
			{Name: testlibHeaderKey, Content: c.testlibHeader, Mode: 0o644},
		},
		ImageRef:     profile.ImageRef,
		Command:      profile.BuildCommand,
		ArtifactName: profile.ArtifactName,
		Limits: execution.Limits{
			CPUTimeMs:   profile.TimeoutMs,
			WallTimeMs:  profile.TimeoutMs * execution.WallTimeMultiplier,
			MemoryMB:    profile.MemoryMB,
			OutputBytes: execution.DefaultCompileOutputLimitBytes,
		},
	})
	if err != nil {
		return execution.CompileResult{}, fmt.Errorf("checker setup failed: %w", err)
	}
	return compileOut, nil
}
