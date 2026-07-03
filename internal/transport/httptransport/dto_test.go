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

func TestToJudgeResponse(t *testing.T) {
	modelResult := model.JudgeResult{
		Status: model.JudgeStatusOK,
		Compile: model.CompileResult{
			Succeeded: true,
			Log:       "ok",
		},
		Cases: []model.JudgeCaseResult{
			{
				Verdict:   model.VerdictOK,
				Stdout:    "42\n",
				TimeUsed:  10,
				ExitCode:  0,
				ExtraInfo: "",
			},
			{
				Verdict:   model.VerdictWA,
				Stdout:    "41\n",
				TimeUsed:  10,
				ExitCode:  0,
				ExtraInfo: "stdout does not match expected output",
			},
		},
		PassedCount: 1,
		TotalCount:  2,
	}

	dto := ToJudgeResponse(modelResult)
	assert.Equal(t, "OK", dto.Status)
	assert.True(t, dto.Compile.Succeeded)
	require.Len(t, dto.Cases, 2)
	assert.Equal(t, "OK", dto.Cases[0].Verdict)
	assert.Equal(t, "WrongAnswer", dto.Cases[1].Verdict)
	assert.Equal(t, 1, dto.PassedCount)
	assert.Equal(t, 2, dto.TotalCount)
}
