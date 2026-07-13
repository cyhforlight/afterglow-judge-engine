// Package httptransport provides HTTP transport layer DTOs and conversions.
package httptransport

import (
	"strings"

	"afterglow-judge-engine/internal/model"
)

// JudgeTestCaseDTO represents one testcase in HTTP request.
type JudgeTestCaseDTO struct {
	InputText          string `json:"inputText"`
	ExpectedOutputText string `json:"expectedOutputText"`
	InputFile          string `json:"inputFile,omitempty"`
	ExpectedOutputFile string `json:"expectedOutputFile,omitempty"`
}

// JudgeRequestDTO represents an HTTP judge request.
type JudgeRequestDTO struct {
	SourceCode  string             `json:"sourceCode"`
	Checker     string             `json:"checker,omitempty"`
	Language    string             `json:"language"`
	TimeLimit   int                `json:"timeLimit"`   // CPU milliseconds
	MemoryLimit int                `json:"memoryLimit"` // megabytes
	TestCases   []JudgeTestCaseDTO `json:"testcases"`
}

// CompileResultDTO represents compile details.
type CompileResultDTO struct {
	Succeeded bool   `json:"succeeded"`
	Log       string `json:"log"`
}

// JudgeCaseResultDTO represents one testcase result.
type JudgeCaseResultDTO struct {
	Verdict    string `json:"verdict"`
	Stdout     string `json:"stdout"`
	TimeUsed   int    `json:"timeUsed"`   // CPU milliseconds
	MemoryUsed int    `json:"memoryUsed"` // megabytes
	ExitCode   int    `json:"exitCode"`
	ExtraInfo  string `json:"extraInfo"`
}

// JudgeResponseDTO represents an HTTP judge response.
type JudgeResponseDTO struct {
	Status      string               `json:"status"`
	Compile     CompileResultDTO     `json:"compile"`
	Cases       []JudgeCaseResultDTO `json:"cases"`
	PassedCount int                  `json:"passedCount"`
	TotalCount  int                  `json:"totalCount"`
}

// ErrorResponseDTO represents an HTTP error response.
type ErrorResponseDTO struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details string `json:"details,omitempty"`
}

// ToModel converts HTTP DTO into model request.
func (dto *JudgeRequestDTO) ToModel() (model.JudgeRequest, error) {
	language, err := model.ParseLanguage(dto.Language)
	if err != nil {
		return model.JudgeRequest{}, err
	}

	testCases := make([]model.JudgeTestCase, 0, len(dto.TestCases))
	for _, testCase := range dto.TestCases {
		testCases = append(testCases, model.JudgeTestCase{
			InputText:          testCase.InputText,
			ExpectedOutput:     testCase.ExpectedOutputText,
			InputFile:          strings.TrimSpace(testCase.InputFile),
			ExpectedOutputFile: strings.TrimSpace(testCase.ExpectedOutputFile),
		})
	}

	return model.JudgeRequest{
		SourceCode:  dto.SourceCode,
		Checker:     strings.TrimSpace(dto.Checker),
		Language:    language,
		TimeLimit:   dto.TimeLimit,
		MemoryLimit: dto.MemoryLimit,
		TestCases:   testCases,
	}, nil
}

// ToJudgeResponse converts model result into response DTO.
func ToJudgeResponse(result model.JudgeResult) JudgeResponseDTO {
	cases := make([]JudgeCaseResultDTO, 0, len(result.Cases))
	for _, caseResult := range result.Cases {
		cases = append(cases, JudgeCaseResultDTO{
			Verdict:    caseResult.Verdict.String(),
			Stdout:     caseResult.Stdout,
			TimeUsed:   caseResult.TimeUsed,
			MemoryUsed: caseResult.MemoryUsed,
			ExitCode:   caseResult.ExitCode,
			ExtraInfo:  caseResult.ExtraInfo,
		})
	}

	return JudgeResponseDTO{
		Status: result.Status.String(),
		Compile: CompileResultDTO{
			Succeeded: result.Compile.Succeeded,
			Log:       result.Compile.Log,
		},
		Cases:       cases,
		PassedCount: result.PassedCount,
		TotalCount:  result.TotalCount,
	}
}
