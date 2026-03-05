package service

import (
	"context"
	"os"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end integration tests - compile and execute

func TestE2E_C_SortProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read source code
	sourceCode, err := os.ReadFile("../../testprograms/c/ac.c")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageC,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Create input file
	inputFile, err := os.CreateTemp("", "test-input-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(inputFile.Name()) }()

	_, err = inputFile.WriteString("5\n10 20 30 40 50\n")
	require.NoError(t, err)
	_ = inputFile.Close()

	// Execute
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      inputFile.Name(),
		Language:       model.LanguageC,
		TimeLimit:      1000,
		MemoryLimit:    128,
	})

	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	assert.Contains(t, execResult.Stdout, "10 20 30 40 50")
}

func TestE2E_CPP_SortProgram(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read source code
	sourceCode, err := os.ReadFile("../../testprograms/cpp/ac.cpp")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Test with data1
	inputFile, err := os.Open("../../testprograms/data1.in")
	require.NoError(t, err)
	defer func() { _ = inputFile.Close() }()

	expectedOutput, err := os.ReadFile("../../testprograms/data1.out")
	require.NoError(t, err)

	// Execute
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      "../../testprograms/data1.in",
		Language:       model.LanguageCPP,
		TimeLimit:      1000,
		MemoryLimit:    128,
	})

	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	assert.Contains(t, execResult.Stdout, string(expectedOutput)[:10])
}

func TestE2E_Java_HelloWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sourceCode := `
public class Main {
    public static void main(String[] args) {
        System.out.println("Hello World");
    }
}
`

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageJava,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Create empty input file
	inputFile, err := os.CreateTemp("", "test-input-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(inputFile.Name()) }()
	_ = inputFile.Close()

	// Execute
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      inputFile.Name(),
		Language:       model.LanguageJava,
		TimeLimit:      2000,
		MemoryLimit:    256,
	})

	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	assert.Contains(t, execResult.Stdout, "Hello World")
}

func TestE2E_Python_SimpleIO(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	sourceCode := `
n = int(input())
numbers = list(map(int, input().split()))
print(sum(numbers))
`

	// Compile (Python just copies the file)
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguagePython,
		SourceCode: sourceCode,
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Create input file
	inputFile, err := os.CreateTemp("", "test-input-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(inputFile.Name()) }()

	_, err = inputFile.WriteString("3\n10 20 30\n")
	require.NoError(t, err)
	_ = inputFile.Close()

	// Execute
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      inputFile.Name(),
		Language:       model.LanguagePython,
		TimeLimit:      2000,
		MemoryLimit:    256,
	})

	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	assert.Contains(t, execResult.Stdout, "60")
}

func TestE2E_C_TLE(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read TLE source code
	sourceCode, err := os.ReadFile("../../testprograms/c/tle.c")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageC,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Create empty input file
	inputFile, err := os.CreateTemp("", "test-input-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(inputFile.Name()) }()
	_ = inputFile.Close()

	// Execute with short time limit
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      inputFile.Name(),
		Language:       model.LanguageC,
		TimeLimit:      100, // 100ms
		MemoryLimit:    128,
	})

	assert.Equal(t, model.VerdictTLE, execResult.Verdict)
}

func TestE2E_C_MLE(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read MLE source code
	sourceCode, err := os.ReadFile("../../testprograms/c/mle.c")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageC,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Create empty input file
	inputFile, err := os.CreateTemp("", "test-input-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(inputFile.Name()) }()
	_ = inputFile.Close()

	// Execute with low memory limit
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      inputFile.Name(),
		Language:       model.LanguageC,
		TimeLimit:      5000,
		MemoryLimit:    64, // 64MB - should trigger MLE
	})

	assert.Equal(t, model.VerdictMLE, execResult.Verdict)
}

func TestE2E_C_RE(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read RE source code
	sourceCode, err := os.ReadFile("../../testprograms/c/re.c")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageC,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Create empty input file
	inputFile, err := os.CreateTemp("", "test-input-*.txt")
	require.NoError(t, err)
	defer func() { _ = os.Remove(inputFile.Name()) }()
	_ = inputFile.Close()

	// Execute
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      inputFile.Name(),
		Language:       model.LanguageC,
		TimeLimit:      1000,
		MemoryLimit:    128,
	})

	assert.Equal(t, model.VerdictRE, execResult.Verdict)
	assert.NotEqual(t, 0, execResult.ExitCode)
}

func TestE2E_AllTestData(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	// Read and compile C++ sort program
	sourceCode, err := os.ReadFile("../../testprograms/cpp/ac.cpp")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Test all data files
	testCases := []struct {
		name       string
		inputFile  string
		outputFile string
	}{
		{"data1", "../../testprograms/data1.in", "../../testprograms/data1.out"},
		{"data2", "../../testprograms/data2.in", "../../testprograms/data2.out"},
		{"data3", "../../testprograms/data3.in", "../../testprograms/data3.out"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			execResult := runner.Execute(ctx, model.ExecuteRequest{
				ExecutablePath: compileOut.ArtifactPath,
				InputPath:      tc.inputFile,
				Language:       model.LanguageCPP,
				TimeLimit:      1000,
				MemoryLimit:    128,
			})

			assert.Equal(t, model.VerdictOK, execResult.Verdict)
			assert.Equal(t, 0, execResult.ExitCode)

			// Just check that output is not empty
			assert.NotEmpty(t, execResult.Stdout, "Output should not be empty")
		})
	}
}

//nolint:funlen
func TestE2E_WrongAnswer_C(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read WA source code (sorts in descending order)
	sourceCode, err := os.ReadFile("../../testprograms/c/wa.c")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageC,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Execute with test data
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      "../../testprograms/data1.in",
		Language:       model.LanguageC,
		TimeLimit:      1000,
		MemoryLimit:    128,
	})

	// Should run successfully but produce wrong output
	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	// Output should be descending, not ascending
	assert.NotContains(t, execResult.Stdout, "0 1 1 1 1 1 1 4 4 5 8 9 9")
}

//nolint:funlen
func TestE2E_WrongAnswer_CPP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read WA source code
	sourceCode, err := os.ReadFile("../../testprograms/cpp/wa.cpp")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Execute with test data
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      "../../testprograms/data2.in",
		Language:       model.LanguageCPP,
		TimeLimit:      1000,
		MemoryLimit:    128,
	})

	// Should run successfully but produce wrong output
	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	// Output should be descending, not ascending
	assert.NotContains(t, execResult.Stdout, "10 20 30 40 50")
}

//nolint:funlen
func TestE2E_WrongAnswer_Python(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read WA source code
	sourceCode, err := os.ReadFile("../../testprograms/python/wa.py")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguagePython,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Execute with test data
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      "../../testprograms/data3.in",
		Language:       model.LanguagePython,
		TimeLimit:      1000,
		MemoryLimit:    128,
	})

	// Should run successfully but produce wrong output
	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	// Output should be descending, not ascending
	assert.NotEmpty(t, execResult.Stdout)
}

//nolint:funlen
func TestE2E_AcceptedAnswer_Java(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read AC source code
	sourceCode, err := os.ReadFile("../../testprograms/java/ac/Main.java")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageJava,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Execute with test data
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      "../../testprograms/data1.in",
		Language:       model.LanguageJava,
		TimeLimit:      2000,
		MemoryLimit:    256,
	})

	// Should produce correct output
	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	assert.Contains(t, execResult.Stdout, "0 1 1 1 1 1 1 4 4 5 8 9 9")
}

//nolint:funlen
func TestE2E_WrongAnswer_Java(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	sb := sandbox.NewContainerdSandbox("")
	compiler := NewContainerCompiler(sb)
	runner := NewContainerdRunner("")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Read WA source code
	sourceCode, err := os.ReadFile("../../testprograms/java/wa/Main.java")
	require.NoError(t, err)

	// Compile
	compileOut, err := compiler.Compile(ctx, CompileRequest{
		Language:   model.LanguageJava,
		SourceCode: string(sourceCode),
	})
	require.NoError(t, err)
	require.NotNil(t, compileOut.Cleanup)
	defer compileOut.Cleanup()

	assert.True(t, compileOut.Result.Succeeded)

	// Execute with test data
	execResult := runner.Execute(ctx, model.ExecuteRequest{
		ExecutablePath: compileOut.ArtifactPath,
		InputPath:      "../../testprograms/data2.in",
		Language:       model.LanguageJava,
		TimeLimit:      2000,
		MemoryLimit:    256,
	})

	// Should run successfully but produce wrong output
	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	// Output should be descending, not ascending
	assert.NotContains(t, execResult.Stdout, "10 20 30 40 50")
}
