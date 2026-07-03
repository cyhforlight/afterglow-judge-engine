package httptransport

import (
	"testing"

	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJudgeRequestDTO_ToModel(t *testing.T) {
	dto := JudgeRequestDTO{
		SourceCode:  "print(42)",
		Checker:     " ncmp ",
		Language:    "py",
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []JudgeTestCaseDTO{
			{InputText: "1\n", ExpectedOutputText: "1\n"},
			{InputText: "2\n", ExpectedOutputText: "2\n"},
		},
	}

	got, err := dto.ToModel()
	require.NoError(t, err)

	assert.Equal(t, model.LanguagePython, got.Language)
	assert.Equal(t, "ncmp", got.Checker)
	require.Len(t, got.TestCases, 2)
}

func TestJudgeRequestDTO_ToModel_InvalidLanguage(t *testing.T) {
	dto := JudgeRequestDTO{
		SourceCode:  "print(42)",
		Language:    "ruby",
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases:   []JudgeTestCaseDTO{{}},
	}

	_, err := dto.ToModel()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported language")
}
