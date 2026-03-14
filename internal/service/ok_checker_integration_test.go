package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/resource"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOKAndChecker_AllTestcases tests all testcases in testdata/ok-and-checker-cases.
//
//nolint:funlen // Table-driven integration test with 20 testcases
func TestOKAndChecker_AllTestcases(t *testing.T) {
	requireServiceIntegrationTest(t)

	// Define testcases to run (now includes 15, 16 with custom checkers)
	testcases := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}

	for _, tcNum := range testcases {
		t.Run(fmt.Sprintf("testcase-%d", tcNum), func(t *testing.T) {
			t.Parallel()
			env := newServiceIntegrationEnv(t, 120*time.Second)

			// Locate testcase directory
			testcaseDir := testdataPath(t, "ok-and-checker-cases", fmt.Sprintf("testcase-%d", tcNum))

			// Find source file and detect language
			sourcePath, lang := findSourceFile(t, testcaseDir)
			sourceCode := readTestdata(t, "ok-and-checker-cases", fmt.Sprintf("testcase-%d", tcNum), filepath.Base(sourcePath))

			// Compile user program
			artifact, result := compileProgram(t, env, lang, sourceCode)
			require.True(t, result.Succeeded, "compilation failed: %s", result.Log)

			// Read test data
			inputData := readTestdata(t, "ok-and-checker-cases", fmt.Sprintf("testcase-%d", tcNum), "data.in")
			expectedOutput := readTestdata(t, "ok-and-checker-cases", fmt.Sprintf("testcase-%d", tcNum), "data.out")

			// Execute user program
			runOut := runUserProgram(t, env, artifact, lang, inputData, 2000, 256)
			require.Equal(t, sandbox.VerdictOK, runOut.Verdict, "execution failed: %v", runOut.Verdict)

			// Compile and run checker
			checkerName := checkerNameMap[tcNum]
			checker := compileCheckerForTestOK(env.ctx, t, checkerName)
			verdict, message := runCheckerForTest(env.ctx, t, checker, inputData, runOut.Stdout, expectedOutput)

			// Assert expected verdict
			expectedVerdict := expectedVerdictMap[tcNum]
			assert.Equal(t, expectedVerdict, verdict,
				"testcase-%d: expected %v, got %v (message: %s)",
				tcNum, expectedVerdict, verdict, message)
		})
	}
}

func compileCheckerForTestOK(ctx context.Context, t *testing.T, checkerName string) model.CompiledArtifact {
	t.Helper()

	var checkerSource []byte
	resourceStore, err := resource.NewBundled()
	require.NoError(t, err)

	// Check if this is an external checker (has path separator)
	if filepath.Base(checkerName) != checkerName {
		// External checker - load from testdata
		testdataRoot := filepath.Join(projectRoot(t), "testdata", "ok-and-checker-cases")
		checkerPath := filepath.Join(testdataRoot, checkerName)
		checkerSource, err = os.ReadFile(checkerPath)
		require.NoError(t, err, "failed to read external checker: %s", checkerPath)
	} else {
		// Builtin checker - load from internal storage
		checkerSource, err = resourceStore.Get(ctx, filepath.ToSlash(filepath.Join("checkers", checkerName)))
		require.NoError(t, err)
	}

	// Load testlib.h from internal storage
	testlibHeader, err := resourceStore.Get(ctx, testlibHeaderKey)
	require.NoError(t, err)

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := NewThrottledCompiler(NewCompiler(sb), testContainerSem)

	profile := checkerProfile()
	out, err := compiler.Compile(ctx, CompileRequest{
		Files: []workspace.File{
			{Name: checkerSourceFileName, Content: checkerSource, Mode: 0o644},
			{Name: testlibHeaderKey, Content: testlibHeader, Mode: 0o644},
		},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand([]string{checkerSourceFileName}),
		ArtifactName: profile.Compile.ArtifactName,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	})
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded, "checker compilation failed: %s", out.Result.Log)

	return *out.Artifact
}
