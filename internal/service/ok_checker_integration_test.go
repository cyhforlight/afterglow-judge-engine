package service

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/resource"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOKAndChecker_AllTestcases(t *testing.T) {
	requireServiceIntegrationTest(t)

	testcases := []struct {
		num     int
		checker string
		want    checkerOutcome
	}{
		{1, "default", checkerAccepted},
		{2, "rcmp6", checkerAccepted},
		{3, "ncmp", checkerAccepted},
		{4, "wcmp", checkerAccepted},
		{5, "lcmp", checkerAccepted},
		{6, "nyesno", checkerAccepted},
		{7, "rcmp6", checkerAccepted},
		{8, "lcmp", checkerAccepted},
		{9, "default", checkerRejected},
		{10, "rcmp6", checkerRejected},
		{11, "ncmp", checkerRejected},
		{12, "wcmp", checkerRejected},
		{13, "lcmp", checkerRejected},
		{14, "nyesno", checkerRejected},
		{15, "external:testcase-15/checker.cpp", checkerAccepted},
		{16, "external:testcase-16/checker.cpp", checkerRejected},
		{17, "ncmp", checkerRejected},
		{18, "rcmp6", checkerRejected},
		{19, "default", checkerRejected},
		{20, "lcmp", checkerRejected},
	}
	externalRoot, err := filepath.Abs(testdataPath("ok-and-checker-cases"))
	require.NoError(t, err)

	for _, tc := range testcases {
		testcaseName := fmt.Sprintf("testcase-%d", tc.num)
		t.Run(testcaseName, func(t *testing.T) {
			t.Parallel()
			env := newServiceIntegrationEnv(t, 120*time.Second)

			testcaseDir := testdataPath("ok-and-checker-cases", testcaseName)
			sourcePath, lang := findSourceFile(t, testcaseDir)
			sourceCode := readTestdata(t, "ok-and-checker-cases", testcaseName, filepath.Base(sourcePath))

			program, result := compileProgram(t, env, lang, sourceCode)
			require.True(t, result.Succeeded, "compilation failed: %s", result.Log)

			inputData := readTestdata(t, "ok-and-checker-cases", testcaseName, "data.in")
			expectedOutput := readTestdata(t, "ok-and-checker-cases", testcaseName, "data.out")

			runOut := runUserProgram(t, env, program, inputData, 2000, 256)
			require.Equal(t, model.VerdictOK, runOut.Verdict, "execution failed: %v", runOut.Verdict)

			externalFS, err := resource.NewExternal(externalRoot)
			require.NoError(t, err)
			checkerModule := newCheckerForTest(t, env.executor, externalFS)
			prepared := prepareCheckerForTest(env.ctx, t, checkerModule, tc.checker)
			checkResult := checkForTest(env.ctx, t, prepared, inputData, runOut.Stdout, expectedOutput)
			assert.Equal(t, tc.want, checkResult.Outcome)
		})
	}
}
