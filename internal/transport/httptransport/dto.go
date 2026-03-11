// Package httptransport provides HTTP transport layer DTOs and conversions.
package httptransport

import (
	"errors"
	"fmt"
	"strings"

	"afterglow-judge-engine/internal/model"
)

// JudgeTestCaseDTO represents one testcase in HTTP request.
type JudgeTestCaseDTO struct {
	Name               string `json:"name"`
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
	TimeLimit   int                `json:"timeLimit"`   // milliseconds
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
	Name       string `json:"name"`
	Verdict    string `json:"verdict"`
	Stdout     string `json:"stdout"`
	TimeUsed   int    `json:"timeUsed"`   // milliseconds
	MemoryUsed int    `json:"memoryUsed"` // megabytes
	ExitCode   int    `json:"exitCode"`
	ExtraInfo  string `json:"extraInfo"`
}

// JudgeResponseDTO represents an HTTP judge response.
type JudgeResponseDTO struct {
	Verdict     string               `json:"verdict"`
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

// Validate checks whether request is valid.
func (dto *JudgeRequestDTO) Validate() error {
	if strings.TrimSpace(dto.SourceCode) == "" {
		return errors.New("sourceCode is required")
	}
	if strings.TrimSpace(dto.Language) == "" {
		return errors.New("language is required")
	}
	if dto.TimeLimit <= 0 {
		return errors.New("timeLimit must be positive")
	}
	if dto.MemoryLimit <= 0 {
		return errors.New("memoryLimit must be positive")
	}
	if len(dto.TestCases) == 0 {
		return errors.New("testcases must not be empty")
	}

	for i, tc := range dto.TestCases {
		if err := tc.ValidateTestCase(i); err != nil {
			return err
		}
		if strings.TrimSpace(tc.Name) == "" {
			continue
		}
		if strings.ContainsRune(tc.Name, '\n') {
			return fmt.Errorf("testcases[%d].name must be single-line", i)
		}
	}

	return nil
}

// ValidateTestCase checks mutual exclusivity of text vs file fields.
func (tc *JudgeTestCaseDTO) ValidateTestCase(index int) error {
	hasInputFile := strings.TrimSpace(tc.InputFile) != ""
	hasOutputFile := strings.TrimSpace(tc.ExpectedOutputFile) != ""

	// If both file fields are provided, check mutual exclusivity
	if hasInputFile && tc.InputText != "" {
		return fmt.Errorf("testcases[%d]: cannot provide both inputText and inputFile", index)
	}
	if hasOutputFile && tc.ExpectedOutputText != "" {
		return fmt.Errorf("testcases[%d]: cannot provide both expectedOutputText and expectedOutputFile", index)
	}

	return nil
}

// ToModel converts HTTP DTO into model request.
func (dto *JudgeRequestDTO) ToModel() (model.JudgeRequest, error) {
	language, err := model.ParseLanguage(dto.Language)
	if err != nil {
		return model.JudgeRequest{}, err
	}

	testCases := make([]model.JudgeTestCase, 0, len(dto.TestCases))
	for idx, testCase := range dto.TestCases {
		name := strings.TrimSpace(testCase.Name)
		if name == "" {
			name = fmt.Sprintf("case-%d", idx+1)
		}
		testCases = append(testCases, model.JudgeTestCase{
			Name:               name,
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
			Name:       caseResult.Name,
			Verdict:    caseResult.Verdict.String(),
			Stdout:     caseResult.Stdout,
			TimeUsed:   caseResult.TimeUsed,
			MemoryUsed: caseResult.MemoryUsed,
			ExitCode:   caseResult.ExitCode,
			ExtraInfo:  caseResult.ExtraInfo,
		})
	}

	return JudgeResponseDTO{
		Verdict: result.Verdict.String(),
		Compile: CompileResultDTO{
			Succeeded: result.Compile.Succeeded,
			Log:       result.Compile.Log,
		},
		Cases:       cases,
		PassedCount: result.PassedCount,
		TotalCount:  result.TotalCount,
	}
}
