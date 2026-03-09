package service

import (
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// End-to-end smoke test - minimal test to verify basic infrastructure.
// For comprehensive integration tests, see:
// - ok_checker_integration_test.go: AC/WA scenarios with checkers
// - sandbox_failure_test.go: CE/TLE/MLE/RE/OLE/POLICY scenarios

func TestE2E_SmokeTest(t *testing.T) {
	requireServiceIntegrationTest(t)

	env := newServiceIntegrationEnv(t, 60*time.Second)

	// Simple C program: read two integers, output their sum
	sourceCode := `
#include <stdio.h>
int main() {
    int a, b;
    scanf("%d %d", &a, &b);
    printf("%d\n", a + b);
    return 0;
}
`

	compileOut := compileProgram(t, env, UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: sourceCode,
	})
	require.True(t, compileOut.Result.Succeeded, "compilation should succeed")

	execResult, err := env.runner.Execute(env.ctx, model.ExecuteRequest{
		Program:     *compileOut.Artifact,
		Input:       "10 20\n",
		Language:    model.LanguageC,
		TimeLimit:   1000,
		MemoryLimit: 128,
	})
	require.NoError(t, err)

	assert.Equal(t, model.VerdictOK, execResult.Verdict)
	assert.Equal(t, 0, execResult.ExitCode)
	assert.Contains(t, execResult.Stdout, "30")
}
