package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/cache"
	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func hasContainerd(t *testing.T) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sb := sandbox.NewContainerdSandbox("", "")
	return sb.PreflightCheck(ctx) == nil
}

func TestContainerCompiler_RealCacheHit(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	sb := sandbox.NewContainerdSandbox("", "")

	cacheStorage, err := cache.New(10)
	require.NoError(t, err)

	compiler := NewUserCodeCompiler(NewCachedCompiler(NewCompiler(sb), cacheStorage))

	req := UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 42; }",
	}

	// Verify cache starts empty
	initialStats := cacheStorage.Len()
	initialEntries := initialStats

	// First compilation (cache miss) — artifact stored in cache
	out1, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out1.Result.Succeeded)
	require.NotNil(t, out1.Artifact)
	artifact1Data := append([]byte(nil), out1.Artifact.Data...)

	// Verify cache now has one more entry
	afterMissStats := cacheStorage.Len()
	assert.Equal(t, initialEntries+1, afterMissStats, "cache should have one new entry after miss")

	// Second compilation (cache hit) — returns same cache path
	out2, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out2.Result.Succeeded)
	require.NotNil(t, out2.Artifact)

	// Verify cache entries unchanged (hit, not new entry)
	afterHitStats := cacheStorage.Len()
	assert.Equal(t, afterMissStats, afterHitStats, "cache hit should not add new entry")

	assert.Equal(t, out1.Artifact.Name, out2.Artifact.Name, "cache hit should preserve artifact name")
	assert.Equal(t, out1.Artifact.Mode, out2.Artifact.Mode, "cache hit should preserve artifact mode")
	assert.Equal(t, artifact1Data, out2.Artifact.Data, "cached artifact should have same content")
}

// TestContainerCompiler_CacheEvictionDoesNotBreakHeldArtifact verifies value semantics survive eviction.
func TestContainerCompiler_CacheEvictionDoesNotBreakHeldArtifact(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create cache with very small capacity (2 entries)
	smallCache, err := cache.New(2)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := NewUserCodeCompiler(NewCachedCompiler(NewCompiler(sb), smallCache))

	// Compile program 1
	req1 := UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 1; }",
	}
	out1, err := compiler.Compile(context.Background(), req1)
	require.NoError(t, err)
	require.True(t, out1.Result.Succeeded)
	require.NotNil(t, out1.Artifact)
	heldArtifact := *out1.Artifact

	// Compile two more programs to trigger eviction of program 1
	req2 := UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 2; }",
	}
	_, err = compiler.Compile(context.Background(), req2)
	require.NoError(t, err)

	req3 := UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 3; }",
	}
	_, err = compiler.Compile(context.Background(), req3)
	require.NoError(t, err)

	// Verify the first returned artifact is still usable after eviction.
	assert.NotEmpty(t, heldArtifact.Data)
	assert.Equal(t, "program", heldArtifact.Name)
}

// TestContainerCompiler_NilCacheStillCompiles tests that cache remains optional.
func TestContainerCompiler_NilCacheStillCompiles(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := NewUserCodeCompiler(NewCompiler(sb))

	req := UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 0; }",
	}

	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)
	assert.NotEmpty(t, out.Artifact.Data)
}

// TestContainerCompiler_WorkspaceCleanedAfterCompile verifies workspace is cleaned up.
func TestContainerCompiler_WorkspaceCleanedAfterCompile(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	testCache, err := cache.New(100)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := NewUserCodeCompiler(NewCachedCompiler(NewCompiler(sb), testCache))

	req := UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 1; }",
	}

	// Track workspace directories before compilation
	tmpDir := os.TempDir()
	beforeEntries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	beforeCount := countJudgeWorkspaces(beforeEntries)

	// Compile — workspace should be cleaned up after function returns
	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)

	// Verify no workspace leak
	afterEntries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	afterCount := countJudgeWorkspaces(afterEntries)

	assert.Equal(t, beforeCount, afterCount, "no workspace should leak after compile")

	assert.NotEmpty(t, out.Artifact.Data, "artifact should be returned by value")
}

// countJudgeWorkspaces counts sandbox workspace directories.
func countJudgeWorkspaces(entries []os.DirEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "sandbox-workspace-") {
			count++
		}
	}
	return count
}

// TestContainerCompiler_CompilationFailure tests that compilation failures are handled correctly.
func TestContainerCompiler_CompilationFailure(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create isolated cache for this test
	testCache, err := cache.New(100)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := NewUserCodeCompiler(NewCachedCompiler(NewCompiler(sb), testCache))

	req := UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main( { return 0; }", // Syntax error
	}

	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err, "Compile should not return error for compilation failure")
	require.False(t, out.Result.Succeeded, "compilation should fail")
	require.NotEmpty(t, out.Result.Log, "should have error log")
	require.Nil(t, out.Artifact, "failed compilation should have no artifact")

	// Verify no workspace leak (Cleanup should be safe to call)

	// Verify cache is empty (failed compilations not cached)
	stats := testCache.Len()
	assert.Equal(t, 0, stats, "failed compilations should not be cached")
}
