package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
)

type serviceIntegrationEnv struct {
	ctx      context.Context
	compiler Compiler
	runner   Runner
	language language
}

// testContainerSem limits concurrent container operations across all tests in this package.
var testContainerSem = semaphore.NewWeighted(8)

func requireServiceIntegrationTest(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	sb, err := sandbox.New("", "")
	if err != nil {
		t.Skipf("service integration environment unavailable: %v", err)
	}
	if err := sb.CheckEnvironment(ctx); err != nil {
		t.Skipf("service integration environment unavailable: %v", err)
	}
}

func newIntegrationContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func newExecutorForTest(t *testing.T) execution.Executor {
	t.Helper()
	sb, err := sandbox.New("", "")
	require.NoError(t, err)
	return execution.NewThrottledExecutor(execution.NewExecutor(sb), testContainerSem)
}

func newServiceIntegrationEnv(t *testing.T, timeout time.Duration) serviceIntegrationEnv {
	t.Helper()

	compiler := NewCompiler(newExecutorForTest(t))
	runner := NewRunner(newExecutorForTest(t))
	return serviceIntegrationEnv{
		ctx:      newIntegrationContext(t, timeout),
		compiler: compiler,
		runner:   runner,
		language: newLanguage(compiler, runner),
	}
}

func compileProgram(
	t *testing.T,
	env serviceIntegrationEnv,
	lang model.Language,
	sourceCode string,
) (compiledProgram, model.CompileResult) {
	t.Helper()

	toolchain, err := env.language.Resolve(lang)
	require.NoError(t, err)
	program, result, err := toolchain.Compile(env.ctx, sourceCode)
	require.NoError(t, err)
	return program, result
}

func testdataPath(elems ...string) string {
	parts := append([]string{"..", "..", "testdata"}, elems...)
	return filepath.Join(parts...)
}

func readTestdata(t *testing.T, elems ...string) string {
	t.Helper()

	content, err := os.ReadFile(testdataPath(elems...))
	require.NoError(t, err)
	return string(content)
}

func findSourceFile(t *testing.T, testcaseDir string) (string, model.Language) {
	t.Helper()

	candidates := []struct {
		name     string
		language model.Language
	}{
		{name: "main.c", language: model.LanguageC},
		{name: "main.cpp", language: model.LanguageCPP},
		{name: "main.py", language: model.LanguagePython},
		{name: "Main.java", language: model.LanguageJava},
	}

	for _, candidate := range candidates {
		path := filepath.Join(testcaseDir, candidate.name)
		if _, err := os.Stat(path); err == nil {
			return path, candidate.language
		}
	}

	t.Fatalf("no source file found in %s", testcaseDir)
	return "", model.LanguageUnknown
}
