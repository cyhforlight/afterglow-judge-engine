package httptransport

import (
	"testing"

	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:funlen // Comprehensive table-driven validation coverage.
func TestJudgeRequestDTO_Validate(t *testing.T) {
	tests := []struct {
		name    string
		dto     JudgeRequestDTO
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid request",
			dto: JudgeRequestDTO{
				SourceCode:  "print(42)",
				Language:    "Python",
				TimeLimit:   1000,
				MemoryLimit: 128,
				TestCases: []JudgeTestCaseDTO{
					{InputText: "", ExpectedOutputText: "42\n"},
				},
			},
		},
		{
			name: "missing sourceCode",
			dto: JudgeRequestDTO{
				Language:    "Python",
				TimeLimit:   1000,
				MemoryLimit: 128,
				TestCases:   []JudgeTestCaseDTO{{}},
			},
			wantErr: true,
			errMsg:  "sourceCode is required",
		},
		{
			name: "missing language",
			dto: JudgeRequestDTO{
				SourceCode:  "print(42)",
				TimeLimit:   1000,
				MemoryLimit: 128,
				TestCases:   []JudgeTestCaseDTO{{}},
			},
			wantErr: true,
			errMsg:  "language is required",
		},
		{
			name: "invalid time limit",
			dto: JudgeRequestDTO{
				SourceCode:  "print(42)",
				Language:    "Python",
				TimeLimit:   0,
				MemoryLimit: 128,
				TestCases:   []JudgeTestCaseDTO{{}},
			},
			wantErr: true,
			errMsg:  "timeLimit must be positive",
		},
		{
			name: "empty testcases",
			dto: JudgeRequestDTO{
				SourceCode:  "print(42)",
				Language:    "Python",
				TimeLimit:   1000,
				MemoryLimit: 128,
				TestCases:   nil,
			},
			wantErr: true,
			errMsg:  "testcases must not be empty",
		},
		{
			name: "both inputText and inputFile",
			dto: JudgeRequestDTO{
				SourceCode:  "print(42)",
				Language:    "Python",
				TimeLimit:   1000,
				MemoryLimit: 128,
				TestCases: []JudgeTestCaseDTO{
					{
						InputText:          "test",
						InputFile:          "file.in",
						ExpectedOutputText: "42\n",
					},
				},
			},
			wantErr: true,
			errMsg:  "cannot provide both inputText and inputFile",
		},
		{
			name: "both expectedOutputText and expectedOutputFile",
			dto: JudgeRequestDTO{
				SourceCode:  "print(42)",
				Language:    "Python",
				TimeLimit:   1000,
				MemoryLimit: 128,
				TestCases: []JudgeTestCaseDTO{
					{
						InputText:          "test",
						ExpectedOutputText: "42\n",
						ExpectedOutputFile: "file.out",
					},
				},
			},
			wantErr: true,
			errMsg:  "cannot provide both expectedOutputText and expectedOutputFile",
		},
		{
			name: "invalid memory limit",
			dto: JudgeRequestDTO{
				SourceCode:  "print(42)",
				Language:    "Python",
				TimeLimit:   1000,
				MemoryLimit: 0,
				TestCases:   []JudgeTestCaseDTO{{}},
			},
			wantErr: true,
			errMsg:  "memoryLimit must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.dto.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				return
			}
			assert.NoError(t, err)
		})
	}
}

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
		Verdict: model.VerdictWA,
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
	assert.Equal(t, "WrongAnswer", dto.Verdict)
	assert.True(t, dto.Compile.Succeeded)
	require.Len(t, dto.Cases, 2)
	assert.Equal(t, "OK", dto.Cases[0].Verdict)
	assert.Equal(t, "WrongAnswer", dto.Cases[1].Verdict)
	assert.Equal(t, 1, dto.PassedCount)
	assert.Equal(t, 2, dto.TotalCount)
}
