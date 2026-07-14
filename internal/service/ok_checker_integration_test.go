package service

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-engine/internal/execution"
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
		want    model.Verdict
	}{
		{1, "default", model.VerdictOK},
		{2, "rcmp6", model.VerdictOK},
		{3, "ncmp", model.VerdictOK},
		{4, "wcmp", model.VerdictOK},
		{5, "lcmp", model.VerdictOK},
		{6, "nyesno", model.VerdictOK},
		{7, "rcmp6", model.VerdictOK},
		{8, "lcmp", model.VerdictOK},
		{9, "default", model.VerdictWA},
		{10, "rcmp6", model.VerdictWA},
		{11, "ncmp", model.VerdictWA},
		{12, "wcmp", model.VerdictWA},
		{13, "lcmp", model.VerdictWA},
		{14, "nyesno", model.VerdictWA},
		{15, "external:testcase-15/checker.cpp", model.VerdictOK},
		{16, "external:testcase-16/checker.cpp", model.VerdictWA},
		{17, "ncmp", model.VerdictWA},
		{18, "rcmp6", model.VerdictWA},
		{19, "default", model.VerdictWA},
		{20, "lcmp", model.VerdictWA},
	}

	for _, tc := range testcases {
		testcaseName := fmt.Sprintf("testcase-%d", tc.num)
		t.Run(testcaseName, func(t *testing.T) {
			t.Parallel()
			env := newServiceIntegrationEnv(t, 120*time.Second)

			testcaseDir := testdataPath("ok-and-checker-cases", testcaseName)
			sourcePath, lang := findSourceFile(t, testcaseDir)
			sourceCode := readTestdata(t, "ok-and-checker-cases", testcaseName, filepath.Base(sourcePath))

			artifact, result := compileProgram(t, env, lang, sourceCode)
			require.True(t, result.Succeeded, "compilation failed: %s", result.Log)

			inputData := readTestdata(t, "ok-and-checker-cases", testcaseName, "data.in")
			expectedOutput := readTestdata(t, "ok-and-checker-cases", testcaseName, "data.out")

			runOut := runUserProgram(t, env, artifact, lang, inputData, 2000, 256)
			require.Equal(t, execution.VerdictOK, runOut.Verdict, "execution failed: %v", runOut.Verdict)

			externalFS, err := resource.NewExternal(testdataPath("ok-and-checker-cases"))
			require.NoError(t, err)
			checkerModule := newCheckerForTest(t, env.compiler, env.runner, externalFS)
			prepared := prepareCheckerForTest(env.ctx, t, checkerModule, tc.checker)
			checkResult := checkForTest(env.ctx, t, prepared, inputData, runOut.Stdout, expectedOutput)
			assert.Equal(t, tc.want, checkResult.Verdict)
		})
	}
}
