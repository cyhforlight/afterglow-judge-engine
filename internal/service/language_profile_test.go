package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"afterglow-judge-engine/internal/model"
)

func TestProfileForLanguage_AllLanguages(t *testing.T) {
	tests := []struct {
		name     string
		language model.Language
		wantErr  bool
	}{
		{"C", model.LanguageC, false},
		{"C++", model.LanguageCPP, false},
		{"Java", model.LanguageJava, false},
		{"Python", model.LanguagePython, false},
		{"Unknown", model.LanguageUnknown, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile, err := ProfileForLanguage(tt.language)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, profile.Compile.ImageRef)
			assert.NotEmpty(t, profile.Run.ImageRef)
		})
	}
}

func TestCProfile(t *testing.T) {
	profile := cProfile()

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
	profile := cppProfile()

	assert.Equal(t, "docker.io/library/gcc:12-bookworm", profile.Compile.ImageRef)
	assert.Equal(t, "program", profile.Compile.ArtifactName)
	assert.Equal(t, []string{"main.cpp"}, profile.Compile.SourceFiles)

	assert.Equal(t, "gcr.io/distroless/static-debian12:latest", profile.Run.ImageRef)
}

func TestJavaProfile(t *testing.T) {
	profile := javaProfile()

	assert.Equal(t, "docker.io/library/eclipse-temurin:21-jdk-jammy", profile.Compile.ImageRef)
	assert.Equal(t, "solution.jar", profile.Compile.ArtifactName)
	assert.Equal(t, []string{"Main.java"}, profile.Compile.SourceFiles)

	assert.Equal(t, "gcr.io/distroless/java21-debian12:latest", profile.Run.ImageRef)
	assert.Equal(t, "solution.jar", profile.Run.ArtifactName)
	assert.Equal(t, 0644, int(profile.Run.FileMode))
}

func TestPythonProfile(t *testing.T) {
	profile := pythonProfile()

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
	profile := cProfile()
	cmd := profile.Compile.BuildCommand([]string{"main.c"})

	expected := []string{"gcc", "-O2", "-pipe", "-static", "-s", "-o", "/work/program", "/work/main.c", "-lm"}
	assert.Equal(t, expected, cmd)
}

func TestBuildCommand_CPP(t *testing.T) {
	profile := cppProfile()
	cmd := profile.Compile.BuildCommand([]string{"main.cpp"})

	expected := []string{"g++", "-std=c++20", "-O2", "-pipe", "-static", "-s", "-o", "/work/program", "/work/main.cpp", "-lm"}
	assert.Equal(t, expected, cmd)
}

func TestBuildCommand_Java(t *testing.T) {
	profile := javaProfile()
	cmd := profile.Compile.BuildCommand([]string{"Main.java"})

	assert.Len(t, cmd, 3)
	assert.Equal(t, "sh", cmd[0])
	assert.Equal(t, "-c", cmd[1])
	assert.Contains(t, cmd[2], "javac")
	assert.Contains(t, cmd[2], "jar")
}

func TestRuntimeCommand_C(t *testing.T) {
	profile := cProfile()
	cmd := profile.Run.RuntimeCommand("/sandbox/program")

	expected := []string{"/sandbox/program"}
	assert.Equal(t, expected, cmd)
}

func TestRuntimeCommand_Python(t *testing.T) {
	profile := pythonProfile()
	cmd := profile.Run.RuntimeCommand("/sandbox/solution.py")

	expected := []string{"python3", "/sandbox/solution.py"}
	assert.Equal(t, expected, cmd)
}

func TestRuntimeCommand_Java(t *testing.T) {
	profile := javaProfile()
	cmd := profile.Run.RuntimeCommand("/sandbox/solution.jar")

	expected := []string{"java", "-Xmx256m", "-Xms64m", "-jar", "/sandbox/solution.jar"}
	assert.Equal(t, expected, cmd)
}
