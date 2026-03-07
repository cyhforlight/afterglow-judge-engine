package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
)

func TestCompileKey_DifferentSourceCode(t *testing.T) {
	profile := sandbox.CProfile()

	key1 := CompileKey("int main() { return 0; }", model.LanguageC, profile)
	key2 := CompileKey("int main() { return 1; }", model.LanguageC, profile)

	assert.NotEqual(t, key1, key2, "different source code should produce different keys")
	assert.Len(t, key1, 64, "SHA256 hash should be 64 hex characters")
}

func TestCompileKey_DifferentLanguage(t *testing.T) {
	sourceCode := "int main() { return 0; }"

	keyC := CompileKey(sourceCode, model.LanguageC, sandbox.CProfile())
	keyCPP := CompileKey(sourceCode, model.LanguageCPP, sandbox.CPPProfile())

	assert.NotEqual(t, keyC, keyCPP, "different languages should produce different keys")
}

func TestCompileKey_SameInputProducesSameKey(t *testing.T) {
	sourceCode := "int main() { return 42; }"
	profile := sandbox.CProfile()

	key1 := CompileKey(sourceCode, model.LanguageC, profile)
	key2 := CompileKey(sourceCode, model.LanguageC, profile)

	assert.Equal(t, key1, key2, "same input should produce same key")
}

func TestCompileKey_DifferentCompilerFlags(t *testing.T) {
	sourceCode := "int main() { return 0; }"

	// C and C++ use different compiler flags (-std=c++20)
	keyC := CompileKey(sourceCode, model.LanguageC, sandbox.CProfile())
	keyCPP := CompileKey(sourceCode, model.LanguageCPP, sandbox.CPPProfile())

	assert.NotEqual(t, keyC, keyCPP, "different compiler flags should produce different keys")
}
