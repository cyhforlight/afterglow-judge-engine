package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"

	"golang.org/x/sync/singleflight"

	"afterglow-judge-engine/internal/cache"
	"afterglow-judge-engine/internal/workspace"
)

// cachedCompiler decorates a Compiler with an LRU cache and singleflight
// deduplication. Concurrent compilations of identical requests are coalesced
// into a single inner.Compile call.
type cachedCompiler struct {
	inner Compiler
	cache *cache.Cache[CompileOutput]
	group singleflight.Group
}

// NewCachedCompiler wraps inner with cache + singleflight.
// If c is nil the decorator is bypassed and inner is returned directly.
func NewCachedCompiler(inner Compiler, c *cache.Cache[CompileOutput]) Compiler {
	if c == nil {
		return inner
	}
	return &cachedCompiler{inner: inner, cache: c}
}

func (c *cachedCompiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	key := computeCacheKey(req)

	// Fast path: cache hit.
	if cached, ok := c.cache.Get(key); ok {
		slog.InfoContext(ctx, "compile cache hit", "key", key[:16])
		return cached, nil
	}

	// Coalesce concurrent compilations of the same key.
	v, err, _ := c.group.Do(key, func() (any, error) {
		// Double-check: another goroutine may have populated the cache.
		if cached, ok := c.cache.Get(key); ok {
			slog.InfoContext(ctx, "compile cache hit after singleflight wait", "key", key[:16])
			return cached, nil
		}

		out, err := c.inner.Compile(ctx, req)
		if err != nil {
			return nil, err
		}

		// Only cache successful compilations that produced an artifact.
		if out.Result.Succeeded && out.Artifact != nil {
			c.cache.Set(key, out)
		}

		return out, nil
	})
	if err != nil {
		return CompileOutput{}, err
	}

	return v.(CompileOutput), nil
}

// computeCacheKey produces a deterministic sha256 digest over the
// CompileRequest fields that affect the compiled output: source files
// (sorted by name), container image, and build command.
// Resource limits and artifact name are execution-time constraints
// that do not change the resulting binary.
func computeCacheKey(req CompileRequest) string {
	sorted := make([]workspace.File, len(req.Files))
	copy(sorted, req.Files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "%s\x00", f.Name)
		h.Write(f.Content)
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "%s\x00", req.ImageRef)
	for _, arg := range req.Command {
		fmt.Fprintf(h, "%s\x00", arg)
	}
	return fmt.Sprintf("compile:%x", h.Sum(nil))
}
