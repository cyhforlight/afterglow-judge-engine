package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"afterglow-judge-sandbox/internal/cache"
	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostCompiler_Compile_PythonSuccess(t *testing.T) {
	compiler := NewHostCompiler()

	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguagePython,
		SourceCode: "print(42)\n",
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded)
	assert.Equal(t, model.LanguagePython, out.RuntimeLanguage)
	assert.Contains(t, out.Result.Log, "does not require compile")
	assert.NotEmpty(t, out.ArtifactPath)

	data, readErr := os.ReadFile(out.ArtifactPath)
	require.NoError(t, readErr)
	assert.Equal(t, "print(42)\n", string(data))
}

func TestHostCompiler_Compile_UnknownLanguage(t *testing.T) {
	compiler := NewHostCompiler()

	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguageUnknown,
		SourceCode: "whatever",
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.False(t, out.Result.Succeeded)
	assert.Empty(t, out.ArtifactPath)
	assert.Contains(t, out.Result.Log, "unsupported language")
}

func TestHostCompiler_Compile_CPPToolchainMissing(t *testing.T) {
	t.Setenv("PATH", "")

	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: "int main(){return 0;}\n",
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.False(t, out.Result.Succeeded)
	assert.Empty(t, out.ArtifactPath)
	assert.Contains(t, out.Result.Log, "g++ not found in PATH")
}

func TestHostCompiler_Compile_JavaToolchainMissing(t *testing.T) {
	t.Setenv("PATH", "")

	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language: model.LanguageJava,
		SourceCode: `public class Main {
	public static void main(String[] args) {
		System.out.println(42);
	}
}`,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.False(t, out.Result.Succeeded)
	assert.Empty(t, out.ArtifactPath)
	assert.True(t, strings.Contains(out.Result.Log, "javac not found") || strings.Contains(out.Result.Log, "jar not found"))
}

func TestHostCompiler_Compile_CPPSyntaxError(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ not available")
	}

	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: "int main( { return 0; }\n",
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.False(t, out.Result.Succeeded)
	assert.Empty(t, out.ArtifactPath)
	assert.NotEmpty(t, strings.TrimSpace(out.Result.Log))
}

func TestHostCompiler_CleanupRemovesWorkDir(t *testing.T) {
	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguagePython,
		SourceCode: "print(1)\n",
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)

	workDir := filepath.Dir(out.ArtifactPath)
	_, statErr := os.Stat(workDir)
	require.NoError(t, statErr)

	out.Cleanup()
	_, statErr = os.Stat(workDir)
	require.Error(t, statErr)
	assert.True(t, os.IsNotExist(statErr))
}

// TestHostCompiler_CPP_RealCompilation tests real C++ compilation with executable verification.
func TestHostCompiler_CPP_RealCompilation(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ not available")
	}

	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language: model.LanguageCPP,
		SourceCode: `#include <iostream>
int main() {
    int n;
    std::cin >> n;
    std::cout << n * 2 << std::endl;
    return 0;
}`,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded, "compilation should succeed")
	assert.NotEmpty(t, out.ArtifactPath)
	assert.Equal(t, model.LanguageCPP, out.RuntimeLanguage)

	// Verify the binary exists and is executable
	info, statErr := os.Stat(out.ArtifactPath)
	require.NoError(t, statErr)
	assert.NotZero(t, info.Mode()&0111, "binary should be executable")
}

// TestHostCompiler_C_RealCompilation tests real C compilation.
func TestHostCompiler_C_RealCompilation(t *testing.T) {
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not available")
	}

	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language: model.LanguageC,
		SourceCode: `#include <stdio.h>
int main() {
    printf("Hello, World!\n");
    return 0;
}`,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded)
	assert.NotEmpty(t, out.ArtifactPath)
	assert.Equal(t, model.LanguageC, out.RuntimeLanguage)

	// Verify the binary exists
	_, statErr := os.Stat(out.ArtifactPath)
	require.NoError(t, statErr)
}

// TestHostCompiler_Java_RealCompilation tests real Java compilation.
func TestHostCompiler_Java_RealCompilation(t *testing.T) {
	if _, err := exec.LookPath("javac"); err != nil {
		t.Skip("javac not available")
	}
	if _, err := exec.LookPath("jar"); err != nil {
		t.Skip("jar not available")
	}

	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language: model.LanguageJava,
		SourceCode: `import java.util.Scanner;
public class Main {
    public static void main(String[] args) {
        Scanner sc = new Scanner(System.in);
        int n = sc.nextInt();
        System.out.println(n * 2);
    }
}`,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded, "compilation should succeed")
	assert.NotEmpty(t, out.ArtifactPath)
	assert.Contains(t, out.ArtifactPath, ".jar", "artifact should be a JAR file")
	assert.Equal(t, model.LanguageJava, out.RuntimeLanguage)

	// Verify the JAR exists
	_, statErr := os.Stat(out.ArtifactPath)
	require.NoError(t, statErr)
}

// TestHostCompiler_Python_MultilineCode tests Python with multiline code.
func TestHostCompiler_Python_MultilineCode(t *testing.T) {
	compiler := NewHostCompiler()
	sourceCode := `import sys

def double(n):
    return n * 2

if __name__ == "__main__":
    n = int(sys.stdin.readline())
    print(double(n))
`
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguagePython,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded)
	assert.NotEmpty(t, out.ArtifactPath)

	// Verify the source code is correctly written
	data, readErr := os.ReadFile(out.ArtifactPath)
	require.NoError(t, readErr)
	assert.Equal(t, sourceCode, string(data))
}

// TestHostCompiler_CPP_CompileErrorDetails tests that compile errors include useful details.
func TestHostCompiler_CPP_CompileErrorDetails(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ not available")
	}

	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: "int main() { undeclared_variable = 42; return 0; }\n",
	})
	require.NoError(t, err, "Compile should not return error, but set Succeeded=false")
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.False(t, out.Result.Succeeded)
	assert.Empty(t, out.ArtifactPath)
	assert.NotEmpty(t, out.Result.Log)
	// Verify the log contains useful error information
	assert.Contains(t, out.Result.Log, "undeclared", "error log should mention undeclared variable")
}

// hasContainerd checks if containerd is available for testing.
func hasContainerd(t *testing.T) bool {
	t.Helper()
	_, err := exec.LookPath("ctr")
	return err == nil
}

// countingSandbox wraps a real sandbox and counts Execute calls.
type countingSandbox struct {
	sandbox.Sandbox
	executeCount int
	mu           sync.Mutex
}

func (c *countingSandbox) Execute(ctx context.Context, req sandbox.ExecuteRequest) (sandbox.ExecuteResult, error) {
	c.mu.Lock()
	c.executeCount++
	c.mu.Unlock()
	return c.Sandbox.Execute(ctx, req)
}

func (c *countingSandbox) getExecuteCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.executeCount
}

// TestContainerCompiler_RealCacheHit tests real cache hit with compilation.
func TestContainerCompiler_RealCacheHit(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	require.NotNil(t, compiler.cache, "cache should be initialized")

	req := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 42; }",
	}

	// Verify cache starts empty
	initialStats := compiler.cache.Stats()
	initialEntries := initialStats.Entries

	// First compilation (cache miss)
	out1, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out1.Result.Succeeded)
	require.NotEmpty(t, out1.ArtifactPath)
	defer out1.Cleanup()

	artifact1Path := out1.ArtifactPath
	artifact1Data, err := os.ReadFile(artifact1Path)
	require.NoError(t, err)

	// Verify cache now has one more entry
	afterMissStats := compiler.cache.Stats()
	assert.Equal(t, initialEntries+1, afterMissStats.Entries, "cache should have one new entry after miss")

	// Second compilation (cache hit)
	out2, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out2.Result.Succeeded)
	require.NotEmpty(t, out2.ArtifactPath)
	defer out2.Cleanup()

	// Verify cache entries unchanged (hit, not new entry)
	afterHitStats := compiler.cache.Stats()
	assert.Equal(t, afterMissStats.Entries, afterHitStats.Entries, "cache hit should not add new entry")

	// Verify artifacts are in different locations (copied to separate workspaces)
	assert.NotEqual(t, artifact1Path, out2.ArtifactPath, "cache hit should copy to new workspace")

	// Verify artifact content is identical
	artifact2Data, err := os.ReadFile(out2.ArtifactPath)
	require.NoError(t, err)
	assert.Equal(t, artifact1Data, artifact2Data, "cached artifact should have same content")

	// Verify both cleanups work independently
	out1.Cleanup()
	_, err = os.Stat(artifact1Path)
	assert.True(t, os.IsNotExist(err), "first workspace should be cleaned up")

	_, err = os.Stat(out2.ArtifactPath)
	assert.NoError(t, err, "second workspace should still exist")
}

// TestContainerCompiler_CacheEvictionDuringJudge tests that eviction doesn't break ongoing judge.
func TestContainerCompiler_CacheEvictionDuringJudge(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create cache with very small capacity (2 entries)
	tmpCacheDir := t.TempDir()
	smallCache, err := cache.NewCompileCacheForTest(tmpCacheDir, 2)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("")
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   smallCache,
	}

	// First compilation (miss) - program 1
	req1 := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 1; }",
	}
	out1, err := compiler.Compile(context.Background(), req1)
	require.NoError(t, err)
	require.True(t, out1.Result.Succeeded)
	defer out1.Cleanup()

	// Second compilation (hit) - same program 1
	out1Hit, err := compiler.Compile(context.Background(), req1)
	require.NoError(t, err)
	require.True(t, out1Hit.Result.Succeeded)

	// Keep reference to cache hit artifact path
	hitArtifactPath := out1Hit.ArtifactPath
	hitArtifactData, err := os.ReadFile(hitArtifactPath)
	require.NoError(t, err)

	// Compile two more programs to trigger eviction of program 1 from cache
	req2 := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 2; }",
	}
	out2, err := compiler.Compile(context.Background(), req2)
	require.NoError(t, err)
	defer out2.Cleanup()

	req3 := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 3; }",
	}
	out3, err := compiler.Compile(context.Background(), req3)
	require.NoError(t, err)
	defer out3.Cleanup()

	// Verify cache hit artifact still exists in its workspace (not affected by cache eviction)
	data, err := os.ReadFile(hitArtifactPath)
	require.NoError(t, err, "cache hit artifact should still exist despite cache eviction")
	assert.Equal(t, hitArtifactData, data, "artifact content should be unchanged")

	// Cleanup cache hit workspace
	out1Hit.Cleanup()
	_, err = os.Stat(hitArtifactPath)
	assert.True(t, os.IsNotExist(err), "workspace should be cleaned up")
}

// TestContainerCompiler_CacheFailureDoesNotBreakCompilation tests graceful degradation.
func TestContainerCompiler_CacheFailureDoesNotBreakCompilation(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create compiler with nil cache (simulates cache initialization failure)
	sb := sandbox.NewContainerdSandbox("")
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   nil, // Cache unavailable
	}

	req := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 0; }",
	}

	// Compilation should succeed even without cache
	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err, "compilation should succeed even when cache is unavailable")
	require.True(t, out.Result.Succeeded, "compilation should succeed")
	require.NotEmpty(t, out.ArtifactPath, "should have artifact path")

	// Verify artifact exists
	info, err := os.Stat(out.ArtifactPath)
	require.NoError(t, err, "artifact should exist")
	assert.NotZero(t, info.Size(), "artifact should not be empty")

	// Get the workspace directory (parent of artifact)
	workspaceDir := filepath.Dir(out.ArtifactPath)

	// Cleanup should remove the workspace
	out.Cleanup()
	_, err = os.Stat(workspaceDir)
	assert.True(t, os.IsNotExist(err), "workspace should be cleaned up after Cleanup()")
}

// TestContainerCompiler_CacheFailureNoWorkspaceLeak verifies no workspace leak when cache unavailable.
func TestContainerCompiler_CacheFailureNoWorkspaceLeak(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create compiler with nil cache
	sb := sandbox.NewContainerdSandbox("")
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   nil,
	}

	req := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 1; }",
	}

	// Track workspace directories before compilation
	tmpDir := os.TempDir()
	beforeEntries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	beforeCount := countJudgeWorkspaces(beforeEntries)

	// Compile and immediately cleanup
	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	out.Cleanup()

	// Verify no workspace leak
	afterEntries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	afterCount := countJudgeWorkspaces(afterEntries)

	assert.Equal(t, beforeCount, afterCount, "no workspace should leak after cleanup")
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

// TestContainerCompiler_ConcurrentCacheFailure tests concurrent compilation with cache unavailable.
func TestContainerCompiler_ConcurrentCacheFailure(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create compiler with nil cache (simulates cache unavailable)
	sb := sandbox.NewContainerdSandbox("")
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   nil, // Cache unavailable - forces fallback path
	}

	req := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 42; }",
	}

	const concurrency = 10
	results := make([]CompileOutput, concurrency)
	errors := make([]error, concurrency)

	// Launch 10 concurrent compilations with cache unavailable
	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			out, err := compiler.Compile(context.Background(), req)
			results[idx] = out
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// Verify all requests succeeded despite cache being unavailable
	for i := range concurrency {
		require.NoError(t, errors[i], "request %d should succeed even with cache unavailable", i)
		require.True(t, results[i].Result.Succeeded, "request %d compilation should succeed", i)
		require.NotEmpty(t, results[i].ArtifactPath, "request %d should have artifact", i)
		defer results[i].Cleanup()
	}

	// Verify all artifacts exist and are valid
	for i := range concurrency {
		info, err := os.Stat(results[i].ArtifactPath)
		require.NoError(t, err, "request %d artifact should exist", i)
		assert.NotZero(t, info.Size(), "request %d artifact should not be empty", i)
	}

	// Verify all artifacts are in different workspaces (isolated)
	paths := make(map[string]bool)
	for _, result := range results {
		paths[result.ArtifactPath] = true
	}
	assert.Len(t, paths, concurrency, "all artifacts should be in different workspaces")
}

// TestContainerCompiler_ConcurrentCompilationDeduplication tests singleflight deduplication.
//
//nolint:funlen // Concurrent test requires setup, execution, and verification
func TestContainerCompiler_ConcurrentCompilationDeduplication(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create isolated cache for this test
	tmpCacheDir := t.TempDir()
	testCache, err := cache.NewCompileCacheForTest(tmpCacheDir, 100)
	require.NoError(t, err)

	// Wrap sandbox with counting wrapper to verify deduplication
	countingSb := &countingSandbox{
		Sandbox: sandbox.NewContainerdSandbox(""),
	}

	compiler := &ContainerCompiler{
		sandbox: countingSb,
		cache:   testCache,
	}

	req := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 99; }",
	}

	const concurrency = 10
	results := make([]CompileOutput, concurrency)
	errors := make([]error, concurrency)

	// Verify cache starts empty
	initialStats := testCache.Stats()
	assert.Equal(t, 0, initialStats.Entries, "cache should start empty")

	// Launch 10 concurrent compilations of the same code
	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			out, err := compiler.Compile(context.Background(), req)
			results[idx] = out
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// Verify all requests succeeded
	for i := range concurrency {
		require.NoError(t, errors[i], "request %d should succeed", i)
		require.True(t, results[i].Result.Succeeded, "request %d compilation should succeed", i)
		require.NotEmpty(t, results[i].ArtifactPath, "request %d should have artifact", i)
		defer results[i].Cleanup()
	}

	// Verify all artifacts exist and are executable
	for i := range concurrency {
		info, err := os.Stat(results[i].ArtifactPath)
		require.NoError(t, err, "request %d artifact should exist", i)
		assert.NotZero(t, info.Size(), "request %d artifact should not be empty", i)
	}

	// Verify all artifacts are in different workspaces (isolated)
	paths := make(map[string]bool)
	for _, result := range results {
		paths[result.ArtifactPath] = true
	}
	assert.Len(t, paths, concurrency, "all artifacts should be in different workspaces")

	// Verify cache has only 1 entry (not 10)
	stats := testCache.Stats()
	assert.Equal(t, 1, stats.Entries, "cache should have only 1 entry despite 10 concurrent requests")

	// CRITICAL: Verify that sandbox.Execute was only called ONCE (not 10 times)
	// This proves that singleflight actually deduplicated the compilations
	executeCount := countingSb.getExecuteCount()
	assert.Equal(t, 1, executeCount, "sandbox.Execute should be called only once due to singleflight deduplication")
}

// TestContainerCompiler_CompilationFailure tests that compilation failures are handled correctly.
func TestContainerCompiler_CompilationFailure(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create isolated cache for this test
	tmpCacheDir := t.TempDir()
	testCache, err := cache.NewCompileCacheForTest(tmpCacheDir, 100)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("")
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   testCache,
	}

	req := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main( { return 0; }", // Syntax error
	}

	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err, "Compile should not return error for compilation failure")
	require.False(t, out.Result.Succeeded, "compilation should fail")
	require.NotEmpty(t, out.Result.Log, "should have error log")
	require.Empty(t, out.ArtifactPath, "failed compilation should have no artifact")

	// Verify no workspace leak (Cleanup should be safe to call)
	if out.Cleanup != nil {
		out.Cleanup()
	}

	// Verify cache is empty (failed compilations not cached)
	stats := testCache.Stats()
	assert.Equal(t, 0, stats.Entries, "failed compilations should not be cached")
}

// TestContainerCompiler_ConcurrentCompilationFailure tests concurrent compilation failures.
func TestContainerCompiler_ConcurrentCompilationFailure(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	// Create isolated cache for this test
	tmpCacheDir := t.TempDir()
	testCache, err := cache.NewCompileCacheForTest(tmpCacheDir, 100)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("")
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   testCache,
	}

	req := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main( { return 0; }", // Syntax error
	}

	const concurrency = 5
	results := make([]CompileOutput, concurrency)
	errors := make([]error, concurrency)

	var wg sync.WaitGroup
	for i := range concurrency {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			out, err := compiler.Compile(context.Background(), req)
			results[idx] = out
			errors[idx] = err
		}(i)
	}

	wg.Wait()

	// Verify all requests got compilation failure (not infrastructure error)
	for i := range concurrency {
		require.NoError(t, errors[i], "request %d should not return error", i)
		require.False(t, results[i].Result.Succeeded, "request %d compilation should fail", i)
		require.NotEmpty(t, results[i].Result.Log, "request %d should have error log", i)
	}
}
