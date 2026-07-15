package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
