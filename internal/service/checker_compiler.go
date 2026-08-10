package service

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
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
	cache         *lru.Cache[[sha256.Size]byte, execution.Artifact]
	group         singleflight.Group
}

type checkerCompilation struct {
	artifact *execution.Artifact
	log      string
}

func newCheckerCompiler(executor execution.Executor, testlibHeader []byte) (*checkerCompiler, error) {
	cache, err := lru.New[[sha256.Size]byte, execution.Artifact](checkerCacheEntries)
	if err != nil {
		return nil, err
	}

	return &checkerCompiler{
		executor:      executor,
		profile:       checkerCompileProfile(),
		testlibHeader: testlibHeader,
		cache:         cache,
	}, nil
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
		Succeeded: compilation.artifact != nil,
		Log:       compilation.log,
	}
	if compilation.artifact == nil {
		return nil, result, nil
	}
	return &compiledChecker{executor: c.executor, artifact: *compilation.artifact}, result, nil
}

func (c *checkerCompiler) compile(ctx context.Context, source []byte) (checkerCompilation, error) {
	// The profile and testlib snapshot are fixed for the lifetime of this cache,
	// so the checker source is the only varying compilation input.
	key := sha256.Sum256(source)
	if artifact, ok := c.cache.Get(key); ok {
		slog.DebugContext(ctx, "checker compile cache hit", "key", hex.EncodeToString(key[:8]))
		return checkerCompilation{artifact: &artifact}, nil
	}

	resultCh := c.group.DoChan(string(key[:]), func() (any, error) {
		compileCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			time.Duration(c.profile.TimeoutMs*execution.WallTimeMultiplier)*time.Millisecond,
		)
		defer cancel()

		if artifact, ok := c.cache.Get(key); ok {
			slog.DebugContext(
				compileCtx,
				"checker compile cache hit after singleflight wait",
				"key",
				hex.EncodeToString(key[:8]),
			)
			return checkerCompilation{artifact: &artifact}, nil
		}

		compilation, err := c.compileUncached(compileCtx, source)
		if err != nil {
			return nil, err
		}
		if compilation.artifact != nil {
			c.cache.Add(key, *compilation.artifact)
		}
		return compilation, nil
	})

	select {
	case <-ctx.Done():
		return checkerCompilation{}, fmt.Errorf("checker setup failed: %w", ctx.Err())
	case result := <-resultCh:
		if result.Err != nil {
			return checkerCompilation{}, result.Err
		}
		return result.Val.(checkerCompilation), nil
	}
}

func (c *checkerCompiler) compileUncached(ctx context.Context, source []byte) (checkerCompilation, error) {
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
		return checkerCompilation{}, fmt.Errorf("checker setup failed: %w", err)
	}
	if compileOut.Artifact == nil {
		message := cmp.Or(strings.TrimSpace(compileOut.Log), "checker compilation failed")
		return checkerCompilation{log: message}, nil
	}
	return checkerCompilation{artifact: compileOut.Artifact}, nil
}
