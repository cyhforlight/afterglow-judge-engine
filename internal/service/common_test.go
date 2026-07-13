package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"afterglow-judge-engine/internal/execution"
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

// testContainerSem limits concurrent container operations across all tests in this package.
var testContainerSem = make(chan struct{}, 8)

var findProjectRoot = sync.OnceValues(func() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		if filepath.Dir(dir) == dir {
			return "", os.ErrNotExist
		}
	}
})

func requireServiceIntegrationTest(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	sb := sandbox.NewContainerdSandbox("", "")
	if err := sb.PreflightCheck(ctx); err != nil {
		t.Skipf("service integration environment unavailable: %v", err)
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()

	root, err := findProjectRoot()
	require.NoError(t, err, "failed to locate project root from current working directory")
	return root
}

func newIntegrationContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func newCompilerForTest(t *testing.T) Compiler {
	t.Helper()
	return NewCompiler(newExecutorForTest(t))
}

func newRunnerForTest(t *testing.T) Runner {
	t.Helper()
	return NewRunner(newExecutorForTest(t))
}

func newExecutorForTest(t *testing.T) execution.Executor {
	t.Helper()
	sb := sandbox.NewContainerdSandbox("", "")
	return execution.NewThrottledExecutor(execution.NewExecutor(sb), testContainerSem)
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
			Name:    profile.Compile.SourceFile,
			Content: []byte(sourceCode),
			Mode:    0o644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand,
		ArtifactName: profile.Compile.ArtifactName,
		Limits: execution.Limits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * execution.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: execution.DefaultCompileOutputLimitBytes,
		},
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
