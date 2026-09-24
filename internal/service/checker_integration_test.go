package service

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/resource"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkerScenario struct {
	checker        string
	expectedOutput string
	acceptedOutput string
	rejectedOutput string
}

func newCheckerForTest(t *testing.T, executor execution.Executor, externalFS fs.FS) checker {
	t.Helper()

	bundledFS, err := resource.NewBundled()
	require.NoError(t, err)
	checkerModule, err := newChecker(executor, bundledFS, externalFS)
	require.NoError(t, err)
	return checkerModule
}

func prepareCheckerForTest(ctx context.Context, t *testing.T, checkerModule checker, reference string) preparedChecker {
	t.Helper()

	choice, err := resolveChecker(reference, "")
	require.NoError(t, err)
	src, err := checkerModule.Source(choice)
	require.NoError(t, err)
	compilation, err := src.Compile(ctx)
	require.NoError(t, err)
	require.True(t, compilation.compile.Succeeded, compilation.compile.Log)
	return compilation.checker
}

func checkForTest(
	ctx context.Context,
	t *testing.T,
	prepared preparedChecker,
	inputText string,
	actualOutput string,
	expectedOutput string,
) checkerResult {
	t.Helper()

	result, err := prepared.Check(ctx, inputText, actualOutput, expectedOutput)
	require.NoError(t, err)
	return result
}

var checkerScenarios = []checkerScenario{
	{
		checker:        "default",
		expectedOutput: "42\n",
		acceptedOutput: "42   \n\n",
		rejectedOutput: "41\n",
	},
	{
		checker:        "fcmp",
		expectedOutput: "alpha\nbeta\n",
		acceptedOutput: "alpha\nbeta\n",
		rejectedOutput: "alpha\ngamma\n",
	},
	{
		checker:        "hcmp",
		expectedOutput: "123456789012345678901234567890\n",
		acceptedOutput: "123456789012345678901234567890\n",
		rejectedOutput: "123456789012345678901234567891\n",
	},
	{
		checker:        "lcmp",
		expectedOutput: "alpha beta gamma\nleft right\n",
		acceptedOutput: "alpha   beta gamma\nleft    right\n",
		rejectedOutput: "alpha beta delta\nleft right\n",
	},
	{
		checker:        "ncmp",
		expectedOutput: "1 -2 3 4\n",
		acceptedOutput: "1 -2 3 4\n",
		rejectedOutput: "1 -2 5 4\n",
	},
	{
		checker:        "nyesno",
		expectedOutput: "YES NO YES\n",
		acceptedOutput: "yes NO yEs\n",
		rejectedOutput: "YES YES YES\n",
	},
	{
		checker:        "rcmp4",
		expectedOutput: "1.0\n",
		acceptedOutput: "1.00005\n",
		rejectedOutput: "1.01\n",
	},
	{
		checker:        "rcmp6",
		expectedOutput: "1.0\n",
		acceptedOutput: "1.0000005\n",
		rejectedOutput: "1.00001\n",
	},
	{
		checker:        "rcmp9",
		expectedOutput: "1.0\n",
		acceptedOutput: "1.0000000005\n",
		rejectedOutput: "1.00001\n",
	},
	{
		checker:        "wcmp",
		expectedOutput: "alpha beta gamma\n",
		acceptedOutput: "alpha   beta\ngamma\n",
		rejectedOutput: "alpha beta delta\n",
	},
	{
		checker:        "yesno",
		expectedOutput: "YES\n",
		acceptedOutput: "yes\n",
		rejectedOutput: "NO\n",
	},
}

func TestChecker_AllBundledCheckers(t *testing.T) {
	requireServiceIntegrationTest(t)

	for _, scenario := range checkerScenarios {
		t.Run(scenario.checker, func(t *testing.T) {
			t.Parallel()
			env := newServiceIntegrationEnv(t, 90*time.Second)
			checkerModule := newCheckerForTest(t, env.executor, nil)
			prepared := prepareCheckerForTest(env.ctx, t, checkerModule, scenario.checker)

			cases := []struct {
				name         string
				actualOutput string
				wantOutcome  checkerOutcome
			}{
				{name: "ok", actualOutput: scenario.acceptedOutput, wantOutcome: checkerAccepted},
				{name: "fail", actualOutput: scenario.rejectedOutput, wantOutcome: checkerRejected},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					result := checkForTest(env.ctx, t, prepared, "", tc.actualOutput, scenario.expectedOutput)
					assert.Equal(t, tc.wantOutcome, result.Outcome)
				})
			}
		})
	}
}
