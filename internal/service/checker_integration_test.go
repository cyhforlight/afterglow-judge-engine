package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/resource"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checkerCase struct {
	name           string
	inputText      string
	actualOutput   string
	expectedOutput string
	wantVerdict    model.Verdict
}

type checkerScenario struct {
	checker  string
	okCase   checkerCase
	failCase checkerCase
}

func newInternalResourceStoreForTest(t *testing.T) ResourceStore {
	t.Helper()

	resourceStore, err := resource.NewBundled()
	require.NoError(t, err)
	return resourceStore
}

// compileCheckerForTest compiles a builtin checker using Compiler directly.
func compileCheckerForTest(ctx context.Context, t *testing.T, checkerName string) model.CompiledArtifact {
	t.Helper()

	resourceStore := newInternalResourceStoreForTest(t)
	checkerSource, err := resourceStore.Get(ctx, filepath.ToSlash(filepath.Join("checkers", checkerName)))
	require.NoError(t, err)

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
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)

	return *out.Artifact
}

// runCheckerForTest runs a compiled checker against one testcase using Runner directly.
func runCheckerForTest(
	ctx context.Context, t *testing.T,
	checker model.CompiledArtifact,
	inputText, actualOutput, expectedOutput string,
) (model.Verdict, string) {
	t.Helper()

	sb := sandbox.NewContainerdSandbox("", "")
	runner := NewThrottledRunner(NewRunner(sb), testContainerSem)

	profile := checkerProfile()
	runOut, err := runner.Run(ctx, RunRequest{
		Files: []workspace.File{
			{Name: profile.Run.ArtifactName, Content: checker.Data, Mode: checker.Mode},
			{Name: checkerInputFileName, Content: []byte(inputText), Mode: 0o644},
			{Name: checkerOutputFileName, Content: []byte(actualOutput), Mode: 0o644},
			{Name: checkerAnswerFileName, Content: []byte(expectedOutput), Mode: 0o644},
		},
		ImageRef: profile.Run.ImageRef,
		Command: []string{
			runMountDir + "/" + profile.Run.ArtifactName,
			runMountDir + "/" + checkerInputFileName,
			runMountDir + "/" + checkerOutputFileName,
			runMountDir + "/" + checkerAnswerFileName,
		},
		Cwd:    runMountDir,
		Limits: checkerRunLimits(),
	})
	require.NoError(t, err)

	// Interpret exit code (same logic as JudgeEngine.runChecker)
	message := strings.TrimSpace(runOut.Stderr)
	if message == "" {
		message = strings.TrimSpace(runOut.Stdout)
	}

	switch runOut.Verdict {
	case sandbox.VerdictTLE, sandbox.VerdictMLE, sandbox.VerdictOLE:
		return model.VerdictUKE, message
	}

	switch runOut.ExitCode {
	case 0:
		if runOut.Verdict != sandbox.VerdictOK {
			return model.VerdictUKE, message
		}
		return model.VerdictOK, message
	case 1, 2:
		return model.VerdictWA, message
	default:
		return model.VerdictUKE, message
	}
}

func checkerScenarios() []checkerScenario {
	return []checkerScenario{
		{
			checker: "default.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "42   \n\n",
				expectedOutput: "42\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "41\n",
				expectedOutput: "42\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "fcmp.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "alpha\nbeta\n",
				expectedOutput: "alpha\nbeta\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "alpha\ngamma\n",
				expectedOutput: "alpha\nbeta\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "hcmp.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "123456789012345678901234567890\n",
				expectedOutput: "123456789012345678901234567890\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "123456789012345678901234567891\n",
				expectedOutput: "123456789012345678901234567890\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "lcmp.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "alpha   beta gamma\nleft    right\n",
				expectedOutput: "alpha beta gamma\nleft right\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "alpha beta delta\nleft right\n",
				expectedOutput: "alpha beta gamma\nleft right\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "ncmp.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "1 -2 3 4\n",
				expectedOutput: "1 -2 3 4\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "1 -2 5 4\n",
				expectedOutput: "1 -2 3 4\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "nyesno.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "yes NO yEs\n",
				expectedOutput: "YES NO YES\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "YES YES YES\n",
				expectedOutput: "YES NO YES\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "rcmp4.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "1.00005\n",
				expectedOutput: "1.0\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "1.01\n",
				expectedOutput: "1.0\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "rcmp6.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "1.0000005\n",
				expectedOutput: "1.0\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "1.00001\n",
				expectedOutput: "1.0\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "rcmp9.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "1.0000000005\n",
				expectedOutput: "1.0\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "1.00001\n",
				expectedOutput: "1.0\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "wcmp.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "alpha   beta\ngamma\n",
				expectedOutput: "alpha beta gamma\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "alpha beta delta\n",
				expectedOutput: "alpha beta gamma\n",
				wantVerdict:    model.VerdictWA,
			},
		},
		{
			checker: "yesno.cpp",
			okCase: checkerCase{
				name:           "ok",
				actualOutput:   "yes\n",
				expectedOutput: "YES\n",
				wantVerdict:    model.VerdictOK,
			},
			failCase: checkerCase{
				name:           "fail",
				actualOutput:   "NO\n",
				expectedOutput: "YES\n",
				wantVerdict:    model.VerdictWA,
			},
		},
	}
}

func TestChecker_AllBundledCheckers(t *testing.T) {
	requireServiceIntegrationTest(t)

	for _, scenario := range checkerScenarios() {
		t.Run(strings.TrimSuffix(scenario.checker, ".cpp"), func(t *testing.T) {
			t.Parallel()
			ctx := newIntegrationContext(t, 90*time.Second)
			checker := compileCheckerForTest(ctx, t, scenario.checker)

			cases := []checkerCase{scenario.okCase, scenario.failCase}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					verdict, _ := runCheckerForTest(ctx, t, checker, tc.inputText, tc.actualOutput, tc.expectedOutput)
					assert.Equal(t, tc.wantVerdict, verdict)
				})
			}
		})
	}
}
