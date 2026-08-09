package service

import (
	"testing"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeLanguageRunResult(t *testing.T) {
	tests := []struct {
		name        string
		language    model.Language
		stderr      string
		wantVerdict execution.Verdict
	}{
		{
			name:        "Java out of memory becomes MLE",
			language:    model.LanguageJava,
			stderr:      "Exception in thread \"main\" java.lang.OutOfMemoryError: Java heap space",
			wantVerdict: execution.VerdictMLE,
		},
		{
			name:        "ordinary Java exception stays RE",
			language:    model.LanguageJava,
			stderr:      "java.lang.NullPointerException",
			wantVerdict: execution.VerdictRE,
		},
		{
			name:        "other languages are unchanged",
			language:    model.LanguageCPP,
			stderr:      "java.lang.OutOfMemoryError",
			wantVerdict: execution.VerdictRE,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeLanguageRunResult(tt.language, execution.RunResult{
				Verdict: execution.VerdictRE,
				Stderr:  tt.stderr,
			})
			assert.Equal(t, tt.wantVerdict, result.Verdict)
		})
	}
}
