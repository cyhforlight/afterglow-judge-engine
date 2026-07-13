package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLanguage_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		data string
		want Language
	}{
		{`"C"`, LanguageC},
		{`"C++"`, LanguageCPP},
		{`"Java"`, LanguageJava},
		{`"Python"`, LanguagePython},
	}
	for _, tt := range tests {
		var got Language
		err := json.Unmarshal([]byte(tt.data), &got)
		require.NoError(t, err)
		assert.Equal(t, tt.want, got)
	}
}

func TestLanguage_UnmarshalJSON_RejectsNonCanonicalValues(t *testing.T) {
	tests := []string{`"c++"`, `"CPP"`, `"java"`, `"py"`, `"Python "`, `"Ruby"`}
	for _, data := range tests {
		var language Language
		err := json.Unmarshal([]byte(data), &language)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected one of C, C++, Java, Python")
	}
}

func TestStringEnums_MarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "language", value: LanguageCPP, want: `"C++"`},
		{name: "unknown language", value: LanguageUnknown, want: `"Unknown"`},
		{name: "verdict", value: VerdictTLE, want: `"TimeLimitExceeded"`},
		{name: "unknown verdict", value: VerdictUnknown, want: `"Unknown"`},
		{name: "judge status", value: JudgeStatusSystemError, want: `"SystemError"`},
		{name: "unknown judge status", value: JudgeStatus(""), want: `"Unknown"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.value)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}
