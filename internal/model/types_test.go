package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLanguage(t *testing.T) {
	tests := []struct {
		raw  string
		want Language
	}{
		{"C", LanguageC},
		{"c++", LanguageCPP},
		{"CPP", LanguageCPP},
		{"Java", LanguageJava},
		{"python", LanguagePython},
		{"py", LanguagePython},
		{"PY3", LanguagePython},
	}
	for _, tt := range tests {
		got, err := ParseLanguage(tt.raw)
		require.NoError(t, err, "ParseLanguage(%q)", tt.raw)
		assert.Equal(t, tt.want, got, "ParseLanguage(%q)", tt.raw)
	}
}

func TestParseLanguageRejectsUnknown(t *testing.T) {
	_, err := ParseLanguage("COBOL")
	assert.Error(t, err)
}
