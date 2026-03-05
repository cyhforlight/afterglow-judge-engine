package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspace_CreateAndCleanup(t *testing.T) {
	ws, err := NewWorkspace()
	require.NoError(t, err)
	require.NotNil(t, ws)

	dir := ws.Dir()
	assert.NotEmpty(t, dir)

	err = ws.Cleanup()
	assert.NoError(t, err)
}

func TestWorkspace_WriteAndReadFile(t *testing.T) {
	ws, err := NewWorkspace()
	require.NoError(t, err)
	defer func() { _ = ws.Cleanup() }()

	content := []byte("hello world")
	err = ws.WriteFile("test.txt", content, 0644)
	require.NoError(t, err)

	readContent, err := ws.ReadFile("test.txt")
	require.NoError(t, err)
	assert.Equal(t, content, readContent)
}

func TestWorkspace_ReadNonExistentFile(t *testing.T) {
	ws, err := NewWorkspace()
	require.NoError(t, err)
	defer func() { _ = ws.Cleanup() }()

	_, err = ws.ReadFile("nonexistent.txt")
	assert.Error(t, err)
}

func TestProfileForLanguage_AllLanguages(t *testing.T) {
	tests := []struct {
		name     string
		language int
		wantErr  bool
	}{
		{"C", 1, false},       // LanguageC
		{"C++", 2, false},     // LanguageCPP
		{"Java", 3, false},    // LanguageJava
		{"Python", 4, false},  // LanguagePython
		{"Unknown", 0, false}, // Returns empty profile
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We can't import model.Language here, so we skip this test
			// This will be tested in integration tests
			t.Skip("Requires model.Language import")
		})
	}
}

func TestCProfile(t *testing.T) {
	profile := CProfile()

	assert.Equal(t, "docker.io/library/gcc:12-bookworm", profile.Compile.ImageRef)
	assert.Equal(t, "program", profile.Compile.ArtifactName)
	assert.Equal(t, []string{"main.c"}, profile.Compile.SourceFiles)
	assert.Equal(t, 30000, profile.Compile.TimeoutMs)
	assert.Equal(t, 512, profile.Compile.MemoryMB)

	assert.Equal(t, "gcr.io/distroless/static-debian12:latest", profile.Run.ImageRef)
	assert.Equal(t, "program", profile.Run.ArtifactName)
	assert.Equal(t, 0755, int(profile.Run.FileMode))
}

func TestCPPProfile(t *testing.T) {
	profile := CPPProfile()

	assert.Equal(t, "docker.io/library/gcc:12-bookworm", profile.Compile.ImageRef)
	assert.Equal(t, "program", profile.Compile.ArtifactName)
	assert.Equal(t, []string{"main.cpp"}, profile.Compile.SourceFiles)

	assert.Equal(t, "gcr.io/distroless/static-debian12:latest", profile.Run.ImageRef)
}

func TestJavaProfile(t *testing.T) {
	profile := JavaProfile()

	assert.Equal(t, "docker.io/library/eclipse-temurin:21-jdk-jammy", profile.Compile.ImageRef)
	assert.Equal(t, "solution.jar", profile.Compile.ArtifactName)
	assert.Equal(t, []string{"Main.java"}, profile.Compile.SourceFiles)

	assert.Equal(t, "gcr.io/distroless/java21-debian12:latest", profile.Run.ImageRef)
	assert.Equal(t, "solution.jar", profile.Run.ArtifactName)
	assert.Equal(t, 0644, int(profile.Run.FileMode))
}

func TestPythonProfile(t *testing.T) {
	profile := PythonProfile()

	// Python now compiles to bytecode
	assert.Equal(t, "docker.io/library/python:3.11-slim-bookworm", profile.Compile.ImageRef)
	assert.Equal(t, "solution.pyc", profile.Compile.ArtifactName)
	assert.Equal(t, []string{"solution.py"}, profile.Compile.SourceFiles)
	assert.Equal(t, 10000, profile.Compile.TimeoutMs)
	assert.Equal(t, 256, profile.Compile.MemoryMB)

	assert.Equal(t, "gcr.io/distroless/python3-debian12:latest", profile.Run.ImageRef)
	assert.Equal(t, "solution.pyc", profile.Run.ArtifactName)
	assert.Equal(t, 0644, int(profile.Run.FileMode))
}

func TestBuildCommand_C(t *testing.T) {
	profile := CProfile()
	cmd := profile.Compile.BuildCommand("/work", []string{"main.c"})

	expected := []string{"gcc", "-O2", "-pipe", "-static", "-s", "-lm", "-o", "/work/program", "/work/main.c"}
	assert.Equal(t, expected, cmd)
}

func TestBuildCommand_CPP(t *testing.T) {
	profile := CPPProfile()
	cmd := profile.Compile.BuildCommand("/work", []string{"main.cpp"})

	expected := []string{"g++", "-std=c++20", "-O2", "-pipe", "-static", "-s", "-lm", "-o", "/work/program", "/work/main.cpp"}
	assert.Equal(t, expected, cmd)
}

func TestBuildCommand_Java(t *testing.T) {
	profile := JavaProfile()
	cmd := profile.Compile.BuildCommand("/work", []string{"Main.java"})

	assert.Len(t, cmd, 3)
	assert.Equal(t, "sh", cmd[0])
	assert.Equal(t, "-c", cmd[1])
	assert.Contains(t, cmd[2], "javac")
	assert.Contains(t, cmd[2], "jar")
}

func TestRuntimeCommand_C(t *testing.T) {
	profile := CProfile()
	cmd := profile.Run.RuntimeCommand("/sandbox/program")

	expected := []string{"/sandbox/program"}
	assert.Equal(t, expected, cmd)
}

func TestRuntimeCommand_Python(t *testing.T) {
	profile := PythonProfile()
	cmd := profile.Run.RuntimeCommand("/sandbox/solution.py")

	expected := []string{"python3", "/sandbox/solution.py"}
	assert.Equal(t, expected, cmd)
}

func TestRuntimeCommand_Java(t *testing.T) {
	profile := JavaProfile()
	cmd := profile.Run.RuntimeCommand("/sandbox/solution.jar")

	expected := []string{"java", "-Xmx256m", "-Xms64m", "-jar", "/sandbox/solution.jar"}
	assert.Equal(t, expected, cmd)
}
