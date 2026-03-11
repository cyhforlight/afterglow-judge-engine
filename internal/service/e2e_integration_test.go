package service

import (
	"strings"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"

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

	artifact, result := compileProgram(t, env, model.LanguageC, sourceCode)
	require.True(t, result.Succeeded, "compilation should succeed")

	profile, err := ProfileForLanguage(model.LanguageC)
	require.NoError(t, err)

	containerPath := runMountDir + "/" + profile.Run.ArtifactName
	runOut, err := env.runner.Run(env.ctx, RunRequest{
		Files: []RunFile{{
			Name:    profile.Run.ArtifactName,
			Content: artifact.Data,
			Mode:    artifact.Mode,
		}},
		ImageRef: profile.Run.ImageRef,
		Command:  profile.Run.RuntimeCommand(containerPath),
		Cwd:      runMountDir,
		Stdin:    strings.NewReader("10 20\n"),
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   1000,
			WallTimeMs:  1000 * sandbox.WallTimeMultiplier,
			MemoryMB:    128,
			OutputBytes: sandbox.DefaultExecutionOutputLimitBytes,
		},
	})
	require.NoError(t, err)

	assert.Equal(t, sandbox.VerdictOK, runOut.Verdict)
	assert.Equal(t, 0, runOut.ExitCode)
	assert.Contains(t, runOut.Stdout, "30")
}
