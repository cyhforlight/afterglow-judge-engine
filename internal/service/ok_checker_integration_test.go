package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
	"afterglow-judge-sandbox/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOKAndChecker_AllTestcases tests all testcases in testdata/ok-and-checker-cases.
// Skips testcase-15 and testcase-16 (custom SPJ).
//
//nolint:funlen // Table-driven integration test with 18 testcases
func TestOKAndChecker_AllTestcases(t *testing.T) {
	requireServiceIntegrationTest(t)

	// Define testcases to run (skip 15, 16 which use custom SPJ)
	testcases := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 17, 18, 19, 20}

	for _, tcNum := range testcases {
		t.Run(fmt.Sprintf("testcase-%d", tcNum), func(t *testing.T) {
			ctx := newIntegrationContext(t, 120*time.Second)

			// Locate testcase directory
			testcaseDir := testdataPath(t, "ok-and-checker-cases", fmt.Sprintf("testcase-%d", tcNum))

			// Find source file and detect language
			sourcePath, lang := findSourceFile(t, testcaseDir)
			sourceCode := readTestdata(t, "ok-and-checker-cases", fmt.Sprintf("testcase-%d", tcNum), filepath.Base(sourcePath))

			// Compile user program
			compiler := newCompilerForTest(t)
			compileReq := UserCodeCompileRequest{
				Language:   lang,
				SourceCode: sourceCode,
			}
			compileOut := compileProgram(t, serviceIntegrationEnv{
				ctx:      ctx,
				compiler: compiler,
				runner:   nil,
			}, compileReq)

			require.True(t, compileOut.Result.Succeeded, "compilation failed: %s", compileOut.Result.Log)

			// Read test data
			inputData := readTestdata(t, "ok-and-checker-cases", fmt.Sprintf("testcase-%d", tcNum), "data.in")
			expectedOutput := readTestdata(t, "ok-and-checker-cases", fmt.Sprintf("testcase-%d", tcNum), "data.out")

			// Execute user program
			runner := newRunnerForTest(t)
			execReq := model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       inputData,
				Language:    lang,
				TimeLimit:   2000,
				MemoryLimit: 256,
			}
			execResult, err := runner.Execute(ctx, execReq)
			require.NoError(t, err)
			require.Equal(t, model.VerdictOK, execResult.Verdict, "execution failed: %v", execResult.Verdict)

			// Compile checker
			checkerName := checkerNameMap[tcNum]
			checker := compileCheckerForTestOK(ctx, t, checkerName)

			// Run checker
			checkerRunner := newCheckerRunnerForTestOK(t)
			checkerReq := CheckerRunRequest{
				Checker:        checker,
				InputText:      inputData,
				ActualOutput:   execResult.Stdout,
				ExpectedOutput: expectedOutput,
			}
			checkerResult, err := checkerRunner.Run(ctx, checkerReq)
			require.NoError(t, err)

			// Assert expected verdict
			expectedVerdict := expectedVerdictMap[tcNum]
			assert.Equal(t, expectedVerdict, checkerResult.Verdict,
				"testcase-%d: expected %v, got %v (message: %s)",
				tcNum, expectedVerdict, checkerResult.Verdict, checkerResult.Message)
		})
	}
}

func newCheckerRunnerForTestOK(t *testing.T) CheckerRunner {
	t.Helper()
	sb := sandbox.NewContainerdSandbox("", "")
	return NewCheckerRunner(NewRunner(sb))
}

func compileCheckerForTestOK(ctx context.Context, t *testing.T, checkerName string) model.CompiledArtifact {
	t.Helper()

	resourceStore, err := storage.NewInternalStorage(filepath.Join(projectRoot(t), "support"))
	require.NoError(t, err)

	checkerSource, err := resourceStore.Get(ctx, filepath.ToSlash(filepath.Join("checkers", checkerName)))
	require.NoError(t, err)

	testlibHeader, err := resourceStore.Get(ctx, "testlib.h")
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("", "")
	cacheDir := t.TempDir()
	cacheStorage, err := storage.NewCacheStorage(cacheDir, 100)
	require.NoError(t, err)

	compiler := NewCheckerCompiler(NewCachedCompiler(NewCompiler(sb), cacheStorage))

	compileReq := CheckerCompileRequest{
		SourceCode: checkerSource,
		SupportFiles: []CompileFile{{
			Name:    "testlib.h",
			Content: testlibHeader,
			Mode:    0o644,
		}},
	}
	compileOut, err := compiler.Compile(ctx, compileReq)
	require.NoError(t, err)
	require.True(t, compileOut.Result.Succeeded, "checker compilation failed: %s", compileOut.Result.Log)

	return *compileOut.Artifact
}
