package service

import (
	"cmp"
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"afterglow-judge-engine/internal/execution"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"
)

// cachedCompiler decorates Executor.Compile with an LRU cache and singleflight
// deduplication while leaving Run unchanged.
type cachedCompiler struct {
	execution.Executor
	cache *lru.Cache[string, execution.CompileResult]
	group singleflight.Group
}

func newCachedCompiler(inner execution.Executor, maxEntries int) (execution.Executor, error) {
	compileCache, err := lru.New[string, execution.CompileResult](maxEntries)
	if err != nil {
		return nil, fmt.Errorf("create compile cache: %w", err)
	}

	return &cachedCompiler{Executor: inner, cache: compileCache}, nil
}

func (c *cachedCompiler) Compile(
	ctx context.Context,
	req execution.CompileRequest,
) (execution.CompileResult, error) {
	key := computeCacheKey(req)

	// Fast path: cache hit.
	if cached, ok := c.cache.Get(key); ok {
		slog.DebugContext(ctx, "compile cache hit", "key", key[:16])
		return cached, nil
	}

	// Coalesce concurrent compilations of the same key.
	resultCh := c.group.DoChan(key, func() (any, error) {
		compileCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			time.Duration(req.Limits.WallTimeMs)*time.Millisecond,
		)
		defer cancel()

		// Double-check: another goroutine may have populated the cache.
		if cached, ok := c.cache.Get(key); ok {
			slog.DebugContext(compileCtx, "compile cache hit after singleflight wait", "key", key[:16])
			return cached, nil
		}

		out, err := c.Executor.Compile(compileCtx, req)
		if err != nil {
			return nil, err
		}

		// Only cache successful compilations.
		if out.Artifact != nil {
			c.cache.Add(key, out)
		}

		return out, nil
	})

	select {
	case <-ctx.Done():
		return execution.CompileResult{}, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return execution.CompileResult{}, result.Err
		}
		return result.Val.(execution.CompileResult), nil
	}
}

// computeCacheKey produces a deterministic sha256 digest over the
// CompileRequest fields that affect the compiled output and its collection:
// source files (sorted by name), container image, build command, and artifact name.
func computeCacheKey(req execution.CompileRequest) string {
	sorted := slices.SortedFunc(slices.Values(req.Files), func(a, b execution.File) int {
		return cmp.Compare(a.Name, b.Name)
	})

	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f.Name))
		h.Write([]byte{0})
		h.Write(f.Content)
		h.Write([]byte{0})
	}
	h.Write([]byte(req.ImageRef))
	h.Write([]byte{0})
	for _, arg := range req.Command {
		h.Write([]byte(arg))
		h.Write([]byte{0})
	}
	h.Write([]byte(req.ArtifactName))
	return fmt.Sprintf("compile:%x", h.Sum(nil))
}
