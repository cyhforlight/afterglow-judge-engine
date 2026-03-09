package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"

	"afterglow-judge-sandbox/internal/cache"
	"afterglow-judge-sandbox/internal/model"
)

// CachedCompiler wraps a Compiler with caching capability.
// It checks cache before compilation and stores artifacts after successful compilation.
type CachedCompiler struct {
	compiler Compiler
	cache    *cache.Cache
}

// NewCachedCompiler creates a compiler with caching.
// If cache is nil, returns the underlying compiler without caching (graceful degradation).
func NewCachedCompiler(compiler Compiler, cache *cache.Cache) Compiler {
	if cache == nil {
		return compiler
	}
	return &CachedCompiler{
		compiler: compiler,
		cache:    cache,
	}
}

// Compile compiles with caching support.
func (c *CachedCompiler) Compile(ctx context.Context, req CompileRequest) (CompileOutput, error) {
	cacheKey := compileCacheKey(req)

	// Try cache first
	if data, ok := c.cache.Get(cacheKey); ok {
		slog.InfoContext(ctx, "compilation cache hit", "key", cacheKey[:16])
		return CompileOutput{
			Result: model.CompileResult{Succeeded: true},
			Artifact: &model.CompiledArtifact{
				Name: req.ArtifactName,
				Data: data,
				Mode: req.ArtifactMode,
			},
		}, nil
	}

	slog.InfoContext(ctx, "compilation cache miss", "key", cacheKey[:16])

	// Cache miss, compile
	out, err := c.compiler.Compile(ctx, req)
	if err != nil {
		return out, err
	}

	// Only cache successful compilations with artifacts
	if !out.Result.Succeeded || out.Artifact == nil {
		return out, nil
	}

	// Store in cache
	c.cache.Set(cacheKey, out.Artifact.Data)

	return out, nil
}

// compileCacheKey generates a deterministic cache key from compile request.
func compileCacheKey(req CompileRequest) string {
	h := sha256.New()
	h.Write([]byte(req.ImageRef))
	h.Write([]byte(req.ArtifactName))
	h.Write([]byte(req.ArtifactPath))
	h.Write([]byte(req.ArtifactMode.String()))
	h.Write([]byte(strings.Join(req.Command, "\x00")))

	for _, file := range req.Files {
		h.Write([]byte(file.Name))
		h.Write([]byte(file.Mode.String()))
		h.Write(file.Content)
	}

	return hex.EncodeToString(h.Sum(nil))
}
