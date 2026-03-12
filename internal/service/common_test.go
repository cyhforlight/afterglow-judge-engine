package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/require"
)

type serviceIntegrationEnv struct {
	ctx      context.Context
	compiler Compiler
	runner   Runner
}

var (
	projectRootOnce   sync.Once
	cachedProjectRoot string
	errProjectRoot    error
)

func requireServiceIntegrationTest(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sb := sandbox.NewContainerdSandbox("", "")
	if err := sb.PreflightCheck(ctx); err != nil {
		t.Skipf("service integration environment unavailable: %v", err)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()

	projectRootOnce.Do(func() {
		wd, err := os.Getwd()
		if err != nil {
			errProjectRoot = err
			return
		}

		dir := wd
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				cachedProjectRoot = dir
				return
			}

			parent := filepath.Dir(dir)
			if parent == dir {
				errProjectRoot = os.ErrNotExist
				return
			}
			dir = parent
		}
	})

	require.NoError(t, errProjectRoot, "failed to locate project root from current working directory")
	return cachedProjectRoot
}

func newIntegrationContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func newCompilerForTest(t *testing.T) Compiler {
	t.Helper()
	sb := sandbox.NewContainerdSandbox("", "")
	return NewCompiler(sb)
}

func newRunnerForTest(t *testing.T) Runner {
	t.Helper()
	sb := sandbox.NewContainerdSandbox("", "")
	return NewRunner(sb)
}

func newServiceIntegrationEnv(t *testing.T, timeout time.Duration) serviceIntegrationEnv {
	t.Helper()

	return serviceIntegrationEnv{
		ctx:      newIntegrationContext(t, timeout),
		compiler: newCompilerForTest(t),
		runner:   newRunnerForTest(t),
	}
}

func compileProgram(t *testing.T, env serviceIntegrationEnv, lang model.Language, sourceCode string) (*model.CompiledArtifact, model.CompileResult) {
	t.Helper()

	profile, err := ProfileForLanguage(lang)
	require.NoError(t, err)

	req := CompileRequest{
		Files: []workspace.File{{
			Name:    profile.Compile.SourceFiles[0],
			Content: []byte(sourceCode),
			Mode:    0644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand(profile.Compile.SourceFiles),
		ArtifactName: profile.Compile.ArtifactName,
		ArtifactMode: profile.Run.FileMode,
		ArtifactPath: profile.Compile.ArtifactName,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	}

	if lang == model.LanguagePython {
		req.ArtifactLoader = loadPythonBytecodeArtifact(profile.Compile.ArtifactName, profile.Run.FileMode)
	}

	out, err := env.compiler.Compile(env.ctx, req)
	require.NoError(t, err)
	return out.Artifact, out.Result
}

// testdataPath constructs absolute path to testdata files.
func testdataPath(t *testing.T, elems ...string) string {
	t.Helper()

	parts := append([]string{projectRoot(t), "testdata"}, elems...)
	return filepath.Join(parts...)
}

// readTestdata reads testdata file content.
func readTestdata(t *testing.T, elems ...string) string {
	t.Helper()

	content, err := os.ReadFile(testdataPath(t, elems...))
	require.NoError(t, err)
	return string(content)
}

// detectLanguageFromFile detects language from file extension.
func detectLanguageFromFile(filename string) model.Language {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".c":
		return model.LanguageC
	case ".cpp":
		return model.LanguageCPP
	case ".java":
		return model.LanguageJava
	case ".py":
		return model.LanguagePython
	default:
		return 0
	}
}

// findSourceFile locates source file in testcase directory.
func findSourceFile(t *testing.T, testcaseDir string) (string, model.Language) {
	t.Helper()

	// Try common source file names
	candidates := []string{"main.c", "main.cpp", "main.py", "Main.java"}

	for _, candidate := range candidates {
		path := filepath.Join(testcaseDir, candidate)
		if _, err := os.Stat(path); err == nil {
			lang := detectLanguageFromFile(candidate)
			require.NotEmpty(t, lang, "failed to detect language for %s", candidate)
			return path, lang
		}
	}

	t.Fatalf("no source file found in %s", testcaseDir)
	return "", 0
}

// checkerNameMap maps testcase number to checker filename.
var checkerNameMap = map[int]string{
	1:  "default.cpp",
	2:  "rcmp6.cpp",
	3:  "ncmp.cpp",
	4:  "wcmp.cpp",
	5:  "lcmp.cpp",
	6:  "nyesno.cpp",
	7:  "rcmp6.cpp",
	8:  "lcmp.cpp",
	9:  "default.cpp",
	10: "rcmp6.cpp",
	11: "ncmp.cpp",
	12: "wcmp.cpp",
	13: "lcmp.cpp",
	14: "nyesno.cpp",
	15: "testcase-15/checker.cpp", // External custom checker
	16: "testcase-16/checker.cpp", // External custom checker
	17: "ncmp.cpp",
	18: "rcmp6.cpp",
	19: "default.cpp",
	20: "lcmp.cpp",
}

// expectedVerdictMap maps testcase number to expected verdict.
var expectedVerdictMap = map[int]model.Verdict{
	1:  model.VerdictOK,
	2:  model.VerdictOK,
	3:  model.VerdictOK,
	4:  model.VerdictOK,
	5:  model.VerdictOK,
	6:  model.VerdictOK,
	7:  model.VerdictOK,
	8:  model.VerdictOK,
	9:  model.VerdictWA,
	10: model.VerdictWA,
	11: model.VerdictWA,
	12: model.VerdictWA,
	13: model.VerdictWA,
	14: model.VerdictWA,
	15: model.VerdictOK, // Custom multiset checker - output is sorted
	16: model.VerdictWA, // Custom sorted checker - output is unsorted
	17: model.VerdictWA,
	18: model.VerdictWA,
	19: model.VerdictWA,
	20: model.VerdictWA,
}
