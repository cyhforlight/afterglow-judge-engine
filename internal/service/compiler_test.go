package service

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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

	assert.True(t, out.Result.Succeeded)
	assert.Equal(t, model.LanguagePython, out.RuntimeLanguage)
	assert.Contains(t, out.Result.Log, "does not require compile")
	require.NotNil(t, out.Artifact)
	assert.Equal(t, "solution.py", out.Artifact.Name)
	assert.Equal(t, []byte("print(42)\n"), out.Artifact.Data)
}

func TestHostCompiler_Compile_UnknownLanguage(t *testing.T) {
	compiler := NewHostCompiler()

	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguageUnknown,
		SourceCode: "whatever",
	})
	require.NoError(t, err)

	assert.False(t, out.Result.Succeeded)
	assert.Nil(t, out.Artifact)
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

	assert.False(t, out.Result.Succeeded)
	assert.Nil(t, out.Artifact)
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

	assert.False(t, out.Result.Succeeded)
	assert.Nil(t, out.Artifact)
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

	assert.False(t, out.Result.Succeeded)
	assert.Nil(t, out.Artifact)
	assert.NotEmpty(t, strings.TrimSpace(out.Result.Log))
}

func TestHostCompiler_ArtifactReturnedByValue(t *testing.T) {
	compiler := NewHostCompiler()
	out, err := compiler.Compile(context.Background(), CompileRequest{
		Language:   model.LanguagePython,
		SourceCode: "print(1)\n",
	})
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)
	assert.Equal(t, []byte("print(1)\n"), out.Artifact.Data)
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

	assert.True(t, out.Result.Succeeded, "compilation should succeed")
	require.NotNil(t, out.Artifact)
	assert.Equal(t, model.LanguageCPP, out.RuntimeLanguage)
	assert.NotEmpty(t, out.Artifact.Data)
	assert.NotZero(t, out.Artifact.Mode&0o111, "binary should be executable")
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

	assert.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)
	assert.Equal(t, model.LanguageC, out.RuntimeLanguage)
	assert.NotEmpty(t, out.Artifact.Data)
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

	assert.True(t, out.Result.Succeeded, "compilation should succeed")
	require.NotNil(t, out.Artifact)
	assert.Equal(t, "solution.jar", out.Artifact.Name)
	assert.Equal(t, model.LanguageJava, out.RuntimeLanguage)
	assert.NotEmpty(t, out.Artifact.Data)
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

	assert.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)
	assert.Equal(t, []byte(sourceCode), out.Artifact.Data)
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

	assert.False(t, out.Result.Succeeded)
	assert.Nil(t, out.Artifact)
	assert.NotEmpty(t, out.Result.Log)
	// Verify the log contains useful error information
	assert.Contains(t, out.Result.Log, "undeclared", "error log should mention undeclared variable")
}

// hasContainerd checks if containerd is available for testing.
func hasContainerd(t *testing.T) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sb := sandbox.NewContainerdSandbox("", "")
	return sb.PreflightCheck(ctx) == nil
}

// TestContainerCompiler_RealCacheHit tests real cache hit with compilation.
func TestContainerCompiler_RealCacheHit(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	sb := sandbox.NewContainerdSandbox("", "")

	cacheDir := t.TempDir()
	compileCache, err := cache.NewCompileCacheForTest(cacheDir, 10)
	require.NoError(t, err)

	compiler := NewContainerCompiler(sb, compileCache)

	req := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 42; }",
	}

	// Verify cache starts empty
	initialStats := compiler.cache.Stats()
	initialEntries := initialStats.Entries

	// First compilation (cache miss) — artifact stored in cache
	out1, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out1.Result.Succeeded)
	require.NotNil(t, out1.Artifact)
	artifact1Data := append([]byte(nil), out1.Artifact.Data...)

	// Verify cache now has one more entry
	afterMissStats := compiler.cache.Stats()
	assert.Equal(t, initialEntries+1, afterMissStats.Entries, "cache should have one new entry after miss")

	// Second compilation (cache hit) — returns same cache path
	out2, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out2.Result.Succeeded)
	require.NotNil(t, out2.Artifact)

	// Verify cache entries unchanged (hit, not new entry)
	afterHitStats := compiler.cache.Stats()
	assert.Equal(t, afterMissStats.Entries, afterHitStats.Entries, "cache hit should not add new entry")

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
	tmpCacheDir := t.TempDir()
	smallCache, err := cache.NewCompileCacheForTest(tmpCacheDir, 2)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   smallCache,
	}

	// Compile program 1
	req1 := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 1; }",
	}
	out1, err := compiler.Compile(context.Background(), req1)
	require.NoError(t, err)
	require.True(t, out1.Result.Succeeded)
	require.NotNil(t, out1.Artifact)
	heldArtifact := *out1.Artifact

	// Compile two more programs to trigger eviction of program 1
	req2 := CompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { return 2; }",
	}
	_, err = compiler.Compile(context.Background(), req2)
	require.NoError(t, err)

	req3 := CompileRequest{
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
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   nil,
	}

	req := CompileRequest{
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

	tmpCacheDir := t.TempDir()
	testCache, err := cache.NewCompileCacheForTest(tmpCacheDir, 100)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := &ContainerCompiler{
		sandbox: sb,
		cache:   testCache,
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
	tmpCacheDir := t.TempDir()
	testCache, err := cache.NewCompileCacheForTest(tmpCacheDir, 100)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("", "")
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
	require.Nil(t, out.Artifact, "failed compilation should have no artifact")

	// Verify no workspace leak (Cleanup should be safe to call)

	// Verify cache is empty (failed compilations not cached)
	stats := testCache.Stats()
	assert.Equal(t, 0, stats.Entries, "failed compilations should not be cached")
}
