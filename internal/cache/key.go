package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"afterglow-judge-sandbox/internal/model"
	"afterglow-judge-sandbox/internal/sandbox"
)

// CompileKey generates a cache key for compilation based on source code,
// language, compiler image, and build command.
func CompileKey(sourceCode string, lang model.Language, profile sandbox.LanguageProfile) string {
	h := sha256.New()
	h.Write([]byte(sourceCode))
	h.Write([]byte(lang.String()))
	h.Write([]byte(profile.Compile.ImageRef))

	buildCmd := profile.Compile.BuildCommand("/work", profile.Compile.SourceFiles)
	h.Write([]byte(strings.Join(buildCmd, "\x00")))

	return hex.EncodeToString(h.Sum(nil))
}
