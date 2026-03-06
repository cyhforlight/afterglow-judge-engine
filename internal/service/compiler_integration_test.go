package service

import (
	"os"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration tests for ContainerCompiler - requires containerd running.
//
//nolint:funlen // Table-driven integration test with multiple language test cases
func TestContainerCompiler_Compile(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name                 string
		language             model.Language
		sourceCode           string
		sourceFixture        []string
		wantSucceeded        bool
		wantArtifact         bool
		wantArtifactEmpty    bool
		wantRuntimeLanguage  model.Language
		checkRuntimeLanguage bool
		wantLogContains      string
		verifyArtifactExists bool
		artifactPathContains string
	}{
		{
			name:     "C success",
			language: model.LanguageC,
			sourceCode: `
#include <stdio.h>
int main() {
    printf("Hello from C\n");
    return 0;
}
`,
			wantSucceeded:        true,
			wantArtifact:         true,
			wantRuntimeLanguage:  model.LanguageC,
			checkRuntimeLanguage: true,
			verifyArtifactExists: true,
		},
		{
			name:     "C compile error",
			language: model.LanguageC,
			sourceCode: `
#include <stdio.h>
int main() {
    int x = 10  // missing semicolon
    printf("%d\n", x);
    return 0;
}
`,
			wantSucceeded:     false,
			wantArtifactEmpty: true,
			wantLogContains:   "error",
		},
		{
			name:     "C++ success",
			language: model.LanguageCPP,
			sourceCode: `
#include <iostream>
using namespace std;
int main() {
    cout << "Hello from C++" << endl;
    return 0;
}
`,
			wantSucceeded:        true,
			wantArtifact:         true,
			wantRuntimeLanguage:  model.LanguageCPP,
			checkRuntimeLanguage: true,
		},
		{
			name:     "C++ compile error",
			language: model.LanguageCPP,
			sourceCode: `
#include <iostream>
using namespace std;
int main() {
    cout << undefinedVariable << endl;
    return 0;
}
`,
			wantSucceeded:   false,
			wantLogContains: "error",
		},
		{
			name:     "Java success",
			language: model.LanguageJava,
			sourceCode: `
public class Main {
    public static void main(String[] args) {
        System.out.println("Hello from Java");
    }
}
`,
			wantSucceeded:        true,
			wantArtifact:         true,
			wantRuntimeLanguage:  model.LanguageJava,
			checkRuntimeLanguage: true,
			verifyArtifactExists: true,
		},
		{
			name:     "Java compile error",
			language: model.LanguageJava,
			sourceCode: `
public class Main {
    public static void main(String[] args) {
        int x = 10  // missing semicolon
        System.out.println(x);
    }
}
`,
			wantSucceeded:   false,
			wantLogContains: "error",
		},
		{
			name:     "Python success",
			language: model.LanguagePython,
			sourceCode: `
print("Hello from Python")
`,
			wantSucceeded:        true,
			wantArtifact:         true,
			wantRuntimeLanguage:  model.LanguagePython,
			checkRuntimeLanguage: true,
			verifyArtifactExists: true,
			artifactPathContains: ".pyc",
		},
		{
			name:          "C fixture accepted",
			language:      model.LanguageC,
			sourceFixture: []string{"c", "ac.c"},
			wantSucceeded: true,
			wantArtifact:  true,
		},
		{
			name:          "C fixture compile error",
			language:      model.LanguageC,
			sourceFixture: []string{"c", "ce.c"},
			wantSucceeded: false,
		},
		{
			name:          "C++ fixture accepted",
			language:      model.LanguageCPP,
			sourceFixture: []string{"cpp", "ac.cpp"},
			wantSucceeded: true,
			wantArtifact:  true,
		},
		{
			name:          "C++ fixture compile error",
			language:      model.LanguageCPP,
			sourceFixture: []string{"cpp", "ce.cpp"},
			wantSucceeded: false,
		},
		{
			name:          "Python fixture accepted",
			language:      model.LanguagePython,
			sourceFixture: []string{"python", "ac.py"},
			wantSucceeded: true,
			wantArtifact:  true,
		},
		{
			name:            "Python fixture compile error",
			language:        model.LanguagePython,
			sourceFixture:   []string{"python", "ce.py"},
			wantSucceeded:   false,
			wantLogContains: "SyntaxError",
		},
		{
			name:            "Java fixture compile error",
			language:        model.LanguageJava,
			sourceFixture:   []string{"java", "ce", "Main.java"},
			wantSucceeded:   false,
			wantLogContains: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceIntegrationEnv(t, 60*time.Second)

			sourceCode := tt.sourceCode
			if len(tt.sourceFixture) > 0 {
				sourceCode = readFixture(t, tt.sourceFixture...)
			}

			out := compileProgram(t, env, CompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})

			assert.Equal(t, tt.wantSucceeded, out.Result.Succeeded)
			if tt.wantArtifact {
				assert.NotEmpty(t, out.ArtifactPath)
			}
			if tt.wantArtifactEmpty {
				assert.Empty(t, out.ArtifactPath)
			}
			if tt.wantLogContains != "" {
				assert.Contains(t, out.Result.Log, tt.wantLogContains)
			}
			if tt.checkRuntimeLanguage {
				assert.Equal(t, tt.wantRuntimeLanguage, out.RuntimeLanguage)
			}
			if tt.verifyArtifactExists {
				_, err := os.Stat(out.ArtifactPath)
				require.NoError(t, err)
			}
			if tt.artifactPathContains != "" {
				assert.Contains(t, out.ArtifactPath, tt.artifactPathContains)
			}
		})
	}
}
