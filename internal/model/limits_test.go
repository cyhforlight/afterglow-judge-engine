package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateJudgeRequest(t *testing.T) {
	limits := JudgeLimits{
		MaxTimeLimitMs: 1000,
		MaxMemoryMB:    128,
		MaxTestCases:   2,
		MaxSourceBytes: 16,
	}

	validReq := JudgeRequest{
		SourceCode:  "print(42)",
		Language:    LanguagePython,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases:   []JudgeTestCase{{InputText: "", ExpectedOutput: ""}},
	}

	tests := []struct {
		name    string
		mutate  func(*JudgeRequest)
		wantErr string
	}{
		{
			name: "valid request",
		},
		{
			name:    "missing source",
			mutate:  func(req *JudgeRequest) { req.SourceCode = "" },
			wantErr: "sourceCode is required",
		},
		{
			name:    "missing language",
			mutate:  func(req *JudgeRequest) { req.Language = LanguageUnknown },
			wantErr: "language is required",
		},
		{
			name:    "unsupported language",
			mutate:  func(req *JudgeRequest) { req.Language = Language("Rust") },
			wantErr: `unsupported language "Rust"; expected one of C, C++, Java, Python`,
		},
		{
			name:    "source too large",
			mutate:  func(req *JudgeRequest) { req.SourceCode = strings.Repeat("x", 17) },
			wantErr: "sourceCode must be at most 16 bytes",
		},
		{
			name:    "time limit too large",
			mutate:  func(req *JudgeRequest) { req.TimeLimit = 1001 },
			wantErr: "timeLimit must be at most 1000 ms",
		},
		{
			name:    "memory limit too large",
			mutate:  func(req *JudgeRequest) { req.MemoryLimit = 129 },
			wantErr: "memoryLimit must be at most 128 MB",
		},
		{
			name: "too many testcases",
			mutate: func(req *JudgeRequest) {
				req.TestCases = []JudgeTestCase{{}, {}, {}}
			},
			wantErr: "testcases must contain at most 2 cases",
		},
		{
			name: "both input text and file",
			mutate: func(req *JudgeRequest) {
				req.TestCases = []JudgeTestCase{{
					InputText: "1\n",
					InputFile: "cases/1.in",
				}}
			},
			wantErr: "testcases[0]: cannot provide both inputText and inputFile",
		},
		{
			name: "both expected output text and file",
			mutate: func(req *JudgeRequest) {
				req.TestCases = []JudgeTestCase{{
					ExpectedOutput:     "1\n",
					ExpectedOutputFile: "cases/1.out",
				}}
			},
			wantErr: "testcases[0]: cannot provide both expectedOutputText and expectedOutputFile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validReq
			req.TestCases = append([]JudgeTestCase(nil), validReq.TestCases...)
			if tt.mutate != nil {
				tt.mutate(&req)
			}

			err := ValidateJudgeRequest(req, limits)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.wantErr, err.Error())
		})
	}
}
