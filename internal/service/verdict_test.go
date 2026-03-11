package service

import (
	"testing"

	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
)

func TestSelectWorstVerdict(t *testing.T) {
	tests := []struct {
		name     string
		cases    []model.JudgeCaseResult
		expected model.Verdict
	}{
		{
			name:     "empty cases returns UKE",
			cases:    []model.JudgeCaseResult{},
			expected: model.VerdictUKE,
		},
		{
			name: "all OK returns OK",
			cases: []model.JudgeCaseResult{
				{Verdict: model.VerdictOK},
				{Verdict: model.VerdictOK},
			},
			expected: model.VerdictOK,
		},
		{
			name: "WA takes priority over OK",
			cases: []model.JudgeCaseResult{
				{Verdict: model.VerdictOK},
				{Verdict: model.VerdictWA},
			},
			expected: model.VerdictWA,
		},
		{
			name: "runtime error takes priority over WA",
			cases: []model.JudgeCaseResult{
				{Verdict: model.VerdictWA},
				{Verdict: model.VerdictRE},
			},
			expected: model.VerdictRE,
		},
		{
			name: "TLE takes priority over RE",
			cases: []model.JudgeCaseResult{
				{Verdict: model.VerdictRE},
				{Verdict: model.VerdictTLE},
			},
			expected: model.VerdictTLE,
		},
		{
			name: "MLE takes priority over TLE",
			cases: []model.JudgeCaseResult{
				{Verdict: model.VerdictTLE},
				{Verdict: model.VerdictMLE},
			},
			expected: model.VerdictMLE,
		},
		{
			name: "OLE takes priority over MLE",
			cases: []model.JudgeCaseResult{
				{Verdict: model.VerdictMLE},
				{Verdict: model.VerdictOLE},
			},
			expected: model.VerdictOLE,
		},
		{
			name: "OLE is highest priority",
			cases: []model.JudgeCaseResult{
				{Verdict: model.VerdictOK},
				{Verdict: model.VerdictWA},
				{Verdict: model.VerdictRE},
				{Verdict: model.VerdictTLE},
				{Verdict: model.VerdictMLE},
				{Verdict: model.VerdictOLE},
			},
			expected: model.VerdictOLE,
		},
		{
			name: "UKE among runtime errors",
			cases: []model.JudgeCaseResult{
				{Verdict: model.VerdictOK},
				{Verdict: model.VerdictUKE},
			},
			expected: model.VerdictUKE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := selectWorstVerdict(tt.cases)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsRuntimeVerdict(t *testing.T) {
	tests := []struct {
		verdict  model.Verdict
		expected bool
	}{
		{model.VerdictOLE, true},
		{model.VerdictMLE, true},
		{model.VerdictTLE, true},
		{model.VerdictRE, true},
		{model.VerdictUKE, true},
		{model.VerdictOK, false},
		{model.VerdictWA, false},
		{model.VerdictUnknown, false},
	}

	for _, tt := range tests {
		t.Run(tt.verdict.String(), func(t *testing.T) {
			result := isRuntimeVerdict(tt.verdict)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRuntimeSeverity(t *testing.T) {
	tests := []struct {
		verdict  model.Verdict
		expected int
	}{
		{model.VerdictOLE, 5},
		{model.VerdictMLE, 4},
		{model.VerdictTLE, 3},
		{model.VerdictRE, 2},
		{model.VerdictUKE, 1},
		{model.VerdictOK, 0},
		{model.VerdictWA, 0},
	}

	for _, tt := range tests {
		t.Run(tt.verdict.String(), func(t *testing.T) {
			result := runtimeSeverity(tt.verdict)
			assert.Equal(t, tt.expected, result)
		})
	}
}
