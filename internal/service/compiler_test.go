package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/workspace"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompiler_RealCompile / TestCompiler_CompilationFailure 已移除：
// 编译成功/失败的行为已被 ok_checker_integration_test 和 sandbox_failure_test 充分覆盖，
// 单独的编译原语测试必要性较低。

func TestCompiler_WorkspaceCleanedAfterCompile(t *testing.T) {
	requireServiceIntegrationTest(t)

	compiler := newCompilerForTest(t)

	profile, err := ProfileForLanguage(model.LanguageC)
	require.NoError(t, err)

	req := CompileRequest{
		Files: []workspace.File{{
			Name:    profile.Compile.SourceFiles[0],
			Content: []byte("int main() { return 1; }"),
			Mode:    0644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand(profile.Compile.SourceFiles),
		ArtifactName: profile.Compile.ArtifactName,
		Limits: sandbox.ResourceLimits{
			CPUTimeMs:   profile.Compile.TimeoutMs,
			WallTimeMs:  profile.Compile.TimeoutMs * sandbox.WallTimeMultiplier,
			MemoryMB:    profile.Compile.MemoryMB,
			OutputBytes: sandbox.DefaultCompileOutputLimitBytes,
		},
	}

	tmpDir := os.TempDir()
	beforeEntries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	beforeCount := countJudgeWorkspaces(beforeEntries)

	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)

	afterEntries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	afterCount := countJudgeWorkspaces(afterEntries)

	assert.Equal(t, beforeCount, afterCount, "no workspace should leak after compile")
	assert.NotEmpty(t, out.Artifact.Data, "artifact should be returned by value")
}

// countJudgeWorkspaces counts sandbox workspace directories.
func countJudgeWorkspaces(entries []os.DirEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "sandbox-workspace-") {
			count++
		}
	}
	return count
}
