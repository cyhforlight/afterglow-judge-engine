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

func TestJavaRuntimeCommandUsesRequestedHeapLimit(t *testing.T) {
	command := javaProfile().Run.RuntimeCommand("/sandbox/solution.jar", RuntimeLimits{MemoryMB: 128})

	assert.Equal(t, []string{
		"java",
		"-Xmx128m",
		"-Xms64m",
		"-jar",
		"/sandbox/solution.jar",
	}, command)
}

func TestSandboxMemoryLimitMB(t *testing.T) {
	tests := []struct {
		name          string
		language      model.Language
		memoryLimitMB int
		wantLimitMB   int
	}{
		{name: "Java adds minimum native reserve", language: model.LanguageJava, memoryLimitMB: 128, wantLimitMB: 192},
		{name: "Java reserve grows with heap", language: model.LanguageJava, memoryLimitMB: 1024, wantLimitMB: 1280},
		{name: "C++ keeps requested limit", language: model.LanguageCPP, memoryLimitMB: 128, wantLimitMB: 128},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantLimitMB, sandboxMemoryLimitMB(tt.language, tt.memoryLimitMB))
		})
	}
}
