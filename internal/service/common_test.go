package service

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/sandbox"
	"afterglow-judge-sandbox/internal/storage"

	"github.com/stretchr/testify/require"
)

type serviceIntegrationEnv struct {
	ctx      context.Context
	compiler UserCodeCompiler
	runner   UserCodeRunner
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

func fixturePath(t *testing.T, elems ...string) string {
	t.Helper()

	parts := append([]string{projectRoot(t), "testprograms"}, elems...)
	return filepath.Join(parts...)
}

func readFixture(t *testing.T, elems ...string) string {
	t.Helper()

	content, err := os.ReadFile(fixturePath(t, elems...))
	require.NoError(t, err)
	return string(content)
}

func newIntegrationContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

func newCompilerForTest(t *testing.T) UserCodeCompiler {
	t.Helper()
	sb := sandbox.NewContainerdSandbox("", "")
	cacheDir := t.TempDir()
	cacheStorage, err := storage.NewCacheStorage(cacheDir, 100)
	require.NoError(t, err)
	return NewUserCodeCompiler(NewCompiler(sb, cacheStorage))
}

func newRunnerForTest(t *testing.T) UserCodeRunner {
	t.Helper()
	sb := sandbox.NewContainerdSandbox("", "")
	return NewUserCodeRunner(NewRunner(sb))
}

func newServiceIntegrationEnv(t *testing.T, timeout time.Duration) serviceIntegrationEnv {
	t.Helper()

	return serviceIntegrationEnv{
		ctx:      newIntegrationContext(t, timeout),
		compiler: newCompilerForTest(t),
		runner:   newRunnerForTest(t),
	}
}

func compileProgram(t *testing.T, env serviceIntegrationEnv, req UserCodeCompileRequest) UserCodeCompileOutput {
	t.Helper()

	out, err := env.compiler.Compile(env.ctx, req)
	require.NoError(t, err)
	return out
}
