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

func TestRuntimeCommand_Java(t *testing.T) {
	profile := javaProfile()
	cmd := profile.Run.RuntimeCommand("/sandbox/solution.jar", RuntimeLimits{MemoryMB: 256})

	expected := []string{"java", "-Xmx192m", "-Xms64m", "-jar", "/sandbox/solution.jar"}
	assert.Equal(t, expected, cmd)
}

func TestJavaHeapLimitMB(t *testing.T) {
	tests := []struct {
		name          string
		memoryLimitMB int
		wantHeapMB    int
	}{
		{name: "standard limit reserves native memory", memoryLimitMB: 256, wantHeapMB: 192},
		{name: "large limit reserves quarter", memoryLimitMB: 1024, wantHeapMB: 768},
		{name: "small limit keeps minimum heap", memoryLimitMB: 32, wantHeapMB: 16},
		{name: "invalid limit keeps minimum heap", memoryLimitMB: 0, wantHeapMB: 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantHeapMB, javaHeapLimitMB(tt.memoryLimitMB))
		})
	}
}
