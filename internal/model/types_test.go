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
