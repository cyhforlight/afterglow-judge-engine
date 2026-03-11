package service

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompiler_RealCompile(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := NewCompiler(sb)

	profile, err := ProfileForLanguage(model.LanguageC)
	require.NoError(t, err)

	req := CompileRequest{
		Files: []CompileFile{{
			Name:    profile.Compile.SourceFiles[0],
			Content: []byte("int main() { return 42; }"),
			Mode:    0644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand(compileMountDir, profile.Compile.SourceFiles),
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

	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err)
	require.True(t, out.Result.Succeeded)
	require.NotNil(t, out.Artifact)
	assert.NotEmpty(t, out.Artifact.Data)
	assert.Equal(t, profile.Compile.ArtifactName, out.Artifact.Name)
}

func TestCompiler_CompilationFailure(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := NewCompiler(sb)

	profile, err := ProfileForLanguage(model.LanguageC)
	require.NoError(t, err)

	req := CompileRequest{
		Files: []CompileFile{{
			Name:    profile.Compile.SourceFiles[0],
			Content: []byte("int main( { return 0; }"), // Syntax error
			Mode:    0644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand(compileMountDir, profile.Compile.SourceFiles),
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

	out, err := compiler.Compile(context.Background(), req)
	require.NoError(t, err, "Compile should not return error for compilation failure")
	require.False(t, out.Result.Succeeded, "compilation should fail")
	require.NotEmpty(t, out.Result.Log, "should have error log")
	require.Nil(t, out.Artifact, "failed compilation should have no artifact")
}

func TestCompiler_WorkspaceCleanedAfterCompile(t *testing.T) {
	if !hasContainerd(t) {
		t.Skip("containerd not available")
	}

	sb := sandbox.NewContainerdSandbox("", "")
	compiler := NewCompiler(sb)

	profile, err := ProfileForLanguage(model.LanguageC)
	require.NoError(t, err)

	req := CompileRequest{
		Files: []CompileFile{{
			Name:    profile.Compile.SourceFiles[0],
			Content: []byte("int main() { return 1; }"),
			Mode:    0644,
		}},
		ImageRef:     profile.Compile.ImageRef,
		Command:      profile.Compile.BuildCommand(compileMountDir, profile.Compile.SourceFiles),
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

func hasContainerd(t *testing.T) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sb := sandbox.NewContainerdSandbox("", "")
	return sb.PreflightCheck(ctx) == nil
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
