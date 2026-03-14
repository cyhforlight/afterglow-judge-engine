package service

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-engine/internal/sandbox"

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
			checker := compileCheckerForTest(env.ctx, t, checkerName, testdataPath(t, "ok-and-checker-cases"))
			verdict, message := runCheckerForTest(env.ctx, t, checker, inputData, runOut.Stdout, expectedOutput)

			// Assert expected verdict
			expectedVerdict := expectedVerdictMap[tcNum]
			assert.Equal(t, expectedVerdict, verdict,
				"testcase-%d: expected %v, got %v (message: %s)",
				tcNum, expectedVerdict, verdict, message)
		})
	}
}

