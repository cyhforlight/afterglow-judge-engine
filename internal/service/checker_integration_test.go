package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
	"afterglow-judge-sandbox/internal/storage"

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

func newCheckerCompilerForTest(t *testing.T) CheckerCompiler {
	t.Helper()

	sb := sandbox.NewContainerdSandbox("", "")
	return NewCheckerCompiler(NewCompiler(sb))
}

func newCheckerRunnerForTest(t *testing.T) CheckerRunner {
	t.Helper()

	sb := sandbox.NewContainerdSandbox("", "")
	return NewCheckerRunner(NewRunner(sb))
}

func newInternalResourceStoreForTest(t *testing.T) ResourceStore {
	t.Helper()

	resourceStore, err := storage.NewInternalStorage(filepath.Join(projectRoot(t), "support"))
	require.NoError(t, err)
	return resourceStore
}

func compileCheckerForTest(ctx context.Context, t *testing.T, checkerName string) model.CompiledArtifact {
	t.Helper()

	resourceStore := newInternalResourceStoreForTest(t)
	checkerSource, err := resourceStore.Get(ctx, filepath.ToSlash(filepath.Join("checkers", checkerName)))
	require.NoError(t, err)

	testlibHeader, err := resourceStore.Get(ctx, testlibHeaderKey)
	require.NoError(t, err)

	out, err := newCheckerCompilerForTest(t).Compile(ctx, CheckerCompileRequest{
		SourceCode: checkerSource,
		SupportFiles: []CompileFile{{
			Name:    testlibHeaderKey,
			Content: testlibHeader,
			Mode:    0o644,
		}},
	})
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)

	return *out.Artifact
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
			ctx := newIntegrationContext(t, 90*time.Second)
			checker := compileCheckerForTest(ctx, t, scenario.checker)
			runner := newCheckerRunnerForTest(t)

			cases := []checkerCase{scenario.okCase, scenario.failCase}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					out, err := runner.Run(ctx, CheckerRunRequest{
						Checker:        checker,
						InputText:      tc.inputText,
						ActualOutput:   tc.actualOutput,
						ExpectedOutput: tc.expectedOutput,
					})
					require.NoError(t, err)
					assert.Equal(t, tc.wantVerdict, out.Verdict)
				})
			}
		})
	}
}
