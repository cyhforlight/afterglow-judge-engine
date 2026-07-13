package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/resource"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkerScenario struct {
	checker        string
	expectedOutput string
	acceptedOutput string
	rejectedOutput string
}

// compileCheckerForTest compiles a checker for testing.
// For builtin checkers (e.g. "default.cpp"), source is loaded from bundled resources.
// For external checkers (path contains separator, e.g. "subdir/checker.cpp"),
// source is loaded from externalDir on the filesystem.
func compileCheckerForTest(ctx context.Context, t *testing.T, checkerName, externalDir string) model.CompiledArtifact {
	t.Helper()

	resourceStore, err := resource.NewBundled()
	require.NoError(t, err)

	var checkerSource []byte
	if filepath.Base(checkerName) != checkerName {
		// External checker — load from filesystem.
		checkerPath := filepath.Join(externalDir, checkerName)
		checkerSource, err = os.ReadFile(checkerPath)
		require.NoError(t, err, "failed to read external checker: %s", checkerPath)
	} else {
		// Builtin checker — load from bundled resources.
		checkerSource, err = resourceStore.Get(ctx, filepath.ToSlash(filepath.Join("checkers", checkerName)))
		require.NoError(t, err)
	}

	testlibHeader, err := resourceStore.Get(ctx, testlibHeaderKey)
	require.NoError(t, err)

	compiler := NewCompiler(newExecutorForTest(t))

	profile := checkerProfile()
	out, err := compiler.Compile(ctx, CompileRequest{
		Files: []workspace.File{
			{Name: profile.Compile.SourceFile, Content: checkerSource, Mode: 0o644},
			{Name: testlibHeaderKey, Content: testlibHeader, Mode: 0o644},
		},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand,
		ArtifactName: profile.Compile.ArtifactName,
		Limits: execution.Limits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * execution.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: execution.DefaultCompileOutputLimitBytes,
		},
	})
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)

	return *out.Artifact
}

func runCheckerForTest(
	ctx context.Context, t *testing.T,
	checker model.CompiledArtifact,
	inputText, actualOutput, expectedOutput string,
) (model.Verdict, string) {
	t.Helper()

	engine := &JudgeEngine{runner: NewRunner(newExecutorForTest(t))}
	verdict, message, err := engine.runChecker(ctx, &checker, inputText, actualOutput, expectedOutput)
	require.NoError(t, err)
	return verdict, message
}

var checkerScenarios = []checkerScenario{
	{
		checker:        "default.cpp",
		expectedOutput: "42\n",
		acceptedOutput: "42   \n\n",
		rejectedOutput: "41\n",
	},
	{
		checker:        "fcmp.cpp",
		expectedOutput: "alpha\nbeta\n",
		acceptedOutput: "alpha\nbeta\n",
		rejectedOutput: "alpha\ngamma\n",
	},
	{
		checker:        "hcmp.cpp",
		expectedOutput: "123456789012345678901234567890\n",
		acceptedOutput: "123456789012345678901234567890\n",
		rejectedOutput: "123456789012345678901234567891\n",
	},
	{
		checker:        "lcmp.cpp",
		expectedOutput: "alpha beta gamma\nleft right\n",
		acceptedOutput: "alpha   beta gamma\nleft    right\n",
		rejectedOutput: "alpha beta delta\nleft right\n",
	},
	{
		checker:        "ncmp.cpp",
		expectedOutput: "1 -2 3 4\n",
		acceptedOutput: "1 -2 3 4\n",
		rejectedOutput: "1 -2 5 4\n",
	},
	{
		checker:        "nyesno.cpp",
		expectedOutput: "YES NO YES\n",
		acceptedOutput: "yes NO yEs\n",
		rejectedOutput: "YES YES YES\n",
	},
	{
		checker:        "rcmp4.cpp",
		expectedOutput: "1.0\n",
		acceptedOutput: "1.00005\n",
		rejectedOutput: "1.01\n",
	},
	{
		checker:        "rcmp6.cpp",
		expectedOutput: "1.0\n",
		acceptedOutput: "1.0000005\n",
		rejectedOutput: "1.00001\n",
	},
	{
		checker:        "rcmp9.cpp",
		expectedOutput: "1.0\n",
		acceptedOutput: "1.0000000005\n",
		rejectedOutput: "1.00001\n",
	},
	{
		checker:        "wcmp.cpp",
		expectedOutput: "alpha beta gamma\n",
		acceptedOutput: "alpha   beta\ngamma\n",
		rejectedOutput: "alpha beta delta\n",
	},
	{
		checker:        "yesno.cpp",
		expectedOutput: "YES\n",
		acceptedOutput: "yes\n",
		rejectedOutput: "NO\n",
	},
}

func TestChecker_AllBundledCheckers(t *testing.T) {
	requireServiceIntegrationTest(t)

	for _, scenario := range checkerScenarios {
		t.Run(strings.TrimSuffix(scenario.checker, ".cpp"), func(t *testing.T) {
			t.Parallel()
			ctx := newIntegrationContext(t, 90*time.Second)
			checker := compileCheckerForTest(ctx, t, scenario.checker, "")

			cases := []struct {
				name         string
				actualOutput string
				wantVerdict  model.Verdict
			}{
				{name: "ok", actualOutput: scenario.acceptedOutput, wantVerdict: model.VerdictOK},
				{name: "fail", actualOutput: scenario.rejectedOutput, wantVerdict: model.VerdictWA},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					verdict, _ := runCheckerForTest(ctx, t, checker, "", tc.actualOutput, scenario.expectedOutput)
					assert.Equal(t, tc.wantVerdict, verdict)
				})
			}
		})
	}
}
