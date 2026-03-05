package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for ContainerCompiler - requires containerd running

func TestContainerCompiler_Compile_C_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sourceCode := `
#include <stdio.h>
int main() {
    printf("Hello from C\n");
    return 0;
}
`

	out, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageC,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded)
	assert.NotEmpty(t, out.ArtifactPath)
	assert.Equal(t, model.LanguageC, out.RuntimeLanguage)

	// Verify artifact exists
	_, err = os.Stat(out.ArtifactPath)
	assert.NoError(t, err)
}

func TestContainerCompiler_Compile_C_CompileError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sourceCode := `
#include <stdio.h>
int main() {
    int x = 10  // missing semicolon
    printf("%d\n", x);
    return 0;
}
`

	out, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageC,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.False(t, out.Result.Succeeded)
	assert.Contains(t, out.Result.Log, "error")
	assert.Empty(t, out.ArtifactPath)
}

func TestContainerCompiler_Compile_CPP_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sourceCode := `
#include <iostream>
using namespace std;
int main() {
    cout << "Hello from C++" << endl;
    return 0;
}
`

	out, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded)
	assert.NotEmpty(t, out.ArtifactPath)
	assert.Equal(t, model.LanguageCPP, out.RuntimeLanguage)
}

func TestContainerCompiler_Compile_CPP_CompileError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sourceCode := `
#include <iostream>
using namespace std;
int main() {
    cout << undefinedVariable << endl;
    return 0;
}
`

	out, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.False(t, out.Result.Succeeded)
	assert.Contains(t, out.Result.Log, "error")
}

func TestContainerCompiler_Compile_Java_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sourceCode := `
public class Main {
    public static void main(String[] args) {
        System.out.println("Hello from Java");
    }
}
`

	out, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageJava,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded)
	assert.NotEmpty(t, out.ArtifactPath)
	assert.Equal(t, model.LanguageJava, out.RuntimeLanguage)

	// Verify JAR exists
	_, err = os.Stat(out.ArtifactPath)
	assert.NoError(t, err)
}

func TestContainerCompiler_Compile_Java_CompileError(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sourceCode := `
public class Main {
    public static void main(String[] args) {
        int x = 10  // missing semicolon
        System.out.println(x);
    }
}
`

	out, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageJava,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.False(t, out.Result.Succeeded)
	assert.Contains(t, out.Result.Log, "error")
}

func TestContainerCompiler_Compile_Python_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sourceCode := `
print("Hello from Python")
`

	out, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguagePython,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, out.Cleanup)
	defer out.Cleanup()

	assert.True(t, out.Result.Succeeded)
	assert.NotEmpty(t, out.ArtifactPath)
	assert.Equal(t, model.LanguagePython, out.RuntimeLanguage)

	// Verify artifact exists (should be a .pyc file)
	_, err = os.Stat(out.ArtifactPath)
	require.NoError(t, err)
	assert.Contains(t, out.ArtifactPath, ".pyc")
}

func TestContainerCompiler_Compile_FromTestPrograms(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)

	tests := []struct {
		name       string
		language   model.Language
		sourceFile string
		shouldFail bool
	}{
		{"C AC", model.LanguageC, "testprograms/c/ac.c", false},
		{"C CE", model.LanguageC, "testprograms/c/ce.c", true},
		{"C++ AC", model.LanguageCPP, "testprograms/cpp/ac.cpp", false},
		{"C++ CE", model.LanguageCPP, "testprograms/cpp/ce.cpp", true},
		{"Python AC", model.LanguagePython, "testprograms/python/ac.py", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceCode, err := os.ReadFile(filepath.Join("../..", tt.sourceFile))
			require.NoError(t, err)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			out, err := compiler.Compile(ctx, CompileRequest{
				Language:   tt.language,
				SourceCode: string(sourceCode),
			})
			require.NoError(t, err)
			require.NotNil(t, out.Cleanup)
			defer out.Cleanup()

			if tt.shouldFail {
				assert.False(t, out.Result.Succeeded, "Expected compilation to fail")
			} else {
				assert.True(t, out.Result.Succeeded, "Expected compilation to succeed")
				assert.NotEmpty(t, out.ArtifactPath)
			}
		})
	}
}
