package service

import (
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"

	"github.com/stretchr/testify/assert"
)

// End-to-end integration tests - compile and execute.
//
//nolint:funlen // Table-driven integration test with multiple language test cases
func TestE2E_AcceptedPrograms(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name          string
		language      model.Language
		sourceText    string
		sourceFixture []string
		inputText     string
		inputFixture  []string
		timeLimit     int
		memoryLimit   int
		assertOutput  func(t *testing.T, stdout string)
	}{
		{
			name:          "C sort program",
			language:      model.LanguageC,
			sourceFixture: []string{"c", "ac.c"},
			inputText:     "5\n10 20 30 40 50\n",
			timeLimit:     1000,
			memoryLimit:   128,
			assertOutput: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "10 20 30 40 50")
			},
		},
		{
			name:          "C++ sort program",
			language:      model.LanguageCPP,
			sourceFixture: []string{"cpp", "ac.cpp"},
			inputFixture:  []string{"data1.in"},
			timeLimit:     1000,
			memoryLimit:   128,
			assertOutput: func(t *testing.T, stdout string) {
				expectedOutput := readFixture(t, "data1.out")
				assert.Contains(t, stdout, expectedOutput[:10])
			},
		},
		{
			name:     "Java hello world",
			language: model.LanguageJava,
			sourceText: `
public class Main {
    public static void main(String[] args) {
        System.out.println("Hello World");
    }
}
`,
			inputText:   "",
			timeLimit:   2000,
			memoryLimit: 256,
			assertOutput: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "Hello World")
			},
		},
		{
			name:     "Python simple IO",
			language: model.LanguagePython,
			sourceText: `
n = int(input())
numbers = list(map(int, input().split()))
print(sum(numbers))
`,
			inputText:   "3\n10 20 30\n",
			timeLimit:   2000,
			memoryLimit: 256,
			assertOutput: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "60")
			},
		},
		{
			name:          "Java accepted answer",
			language:      model.LanguageJava,
			sourceFixture: []string{"java", "ac", "Main.java"},
			inputFixture:  []string{"data1.in"},
			timeLimit:     2000,
			memoryLimit:   256,
			assertOutput: func(t *testing.T, stdout string) {
				assert.Contains(t, stdout, "0 1 1 1 1 1 1 4 4 5 8 9 9")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceIntegrationEnv(t, 90*time.Second)

			sourceCode := tt.sourceText
			if len(tt.sourceFixture) > 0 {
				sourceCode = readFixture(t, tt.sourceFixture...)
			}

			compileOut := compileProgram(t, env, CompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})
			assert.True(t, compileOut.Result.Succeeded)

			inputPath := fixturePath(t, tt.inputFixture...)
			if len(tt.inputFixture) == 0 {
				inputPath = writeTempInputFile(t, tt.inputText)
			}

			execResult := env.runner.Execute(env.ctx, model.ExecuteRequest{
				ExecutablePath: compileOut.ArtifactPath,
				InputPath:      inputPath,
				Language:       tt.language,
				TimeLimit:      tt.timeLimit,
				MemoryLimit:    tt.memoryLimit,
			})

			assert.Equal(t, model.VerdictOK, execResult.Verdict)
			assert.Equal(t, 0, execResult.ExitCode)
			tt.assertOutput(t, execResult.Stdout)
		})
	}
}

//nolint:funlen // Table-driven integration test with multiple runtime limit test cases
func TestE2E_RuntimeLimits(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name                string
		language            model.Language
		sourceFixture       []string
		timeLimit           int
		memoryLimit         int
		wantVerdict         model.Verdict
		wantNonZeroExitCode bool
	}{
		{
			name:          "C TLE",
			language:      model.LanguageC,
			sourceFixture: []string{"c", "tle.c"},
			timeLimit:     100,
			memoryLimit:   128,
			wantVerdict:   model.VerdictTLE,
		},
		{
			name:          "C MLE",
			language:      model.LanguageC,
			sourceFixture: []string{"c", "mle.c"},
			timeLimit:     5000,
			memoryLimit:   64,
			wantVerdict:   model.VerdictMLE,
		},
		{
			name:                "C RE",
			language:            model.LanguageC,
			sourceFixture:       []string{"c", "re.c"},
			timeLimit:           1000,
			memoryLimit:         128,
			wantVerdict:         model.VerdictRE,
			wantNonZeroExitCode: true,
		},
		{
			name:          "C++ TLE",
			language:      model.LanguageCPP,
			sourceFixture: []string{"cpp", "tle.cpp"},
			timeLimit:     100,
			memoryLimit:   128,
			wantVerdict:   model.VerdictTLE,
		},
		{
			name:          "C++ MLE",
			language:      model.LanguageCPP,
			sourceFixture: []string{"cpp", "mle.cpp"},
			timeLimit:     5000,
			memoryLimit:   64,
			wantVerdict:   model.VerdictMLE,
		},
		{
			name:                "C++ RE",
			language:            model.LanguageCPP,
			sourceFixture:       []string{"cpp", "re.cpp"},
			timeLimit:           1000,
			memoryLimit:         128,
			wantVerdict:         model.VerdictRE,
			wantNonZeroExitCode: true,
		},
		{
			name:          "Java TLE",
			language:      model.LanguageJava,
			sourceFixture: []string{"java", "tle", "Main.java"},
			timeLimit:     100,
			memoryLimit:   256,
			wantVerdict:   model.VerdictTLE,
		},
		{
			name:          "Java MLE",
			language:      model.LanguageJava,
			sourceFixture: []string{"java", "mle", "Main.java"},
			timeLimit:     5000,
			memoryLimit:   64,
			wantVerdict:   model.VerdictMLE,
		},
		{
			name:                "Java RE",
			language:            model.LanguageJava,
			sourceFixture:       []string{"java", "re", "Main.java"},
			timeLimit:           2000,
			memoryLimit:         256,
			wantVerdict:         model.VerdictRE,
			wantNonZeroExitCode: true,
		},
		{
			name:          "Python TLE",
			language:      model.LanguagePython,
			sourceFixture: []string{"python", "tle.py"},
			timeLimit:     100,
			memoryLimit:   256,
			wantVerdict:   model.VerdictTLE,
		},
		{
			name:          "Python MLE",
			language:      model.LanguagePython,
			sourceFixture: []string{"python", "mle.py"},
			timeLimit:     5000,
			memoryLimit:   64,
			wantVerdict:   model.VerdictMLE,
		},
		{
			name:                "Python RE",
			language:            model.LanguagePython,
			sourceFixture:       []string{"python", "runtime_error.py"},
			timeLimit:           2000,
			memoryLimit:         256,
			wantVerdict:         model.VerdictRE,
			wantNonZeroExitCode: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceIntegrationEnv(t, 90*time.Second)

			compileOut := compileProgram(t, env, CompileRequest{
				Language:   tt.language,
				SourceCode: readFixture(t, tt.sourceFixture...),
			})
			assert.True(t, compileOut.Result.Succeeded)

			execResult := env.runner.Execute(env.ctx, model.ExecuteRequest{
				ExecutablePath: compileOut.ArtifactPath,
				InputPath:      writeTempInputFile(t, ""),
				Language:       tt.language,
				TimeLimit:      tt.timeLimit,
				MemoryLimit:    tt.memoryLimit,
			})

			assert.Equal(t, tt.wantVerdict, execResult.Verdict)
			if tt.wantNonZeroExitCode {
				assert.NotEqual(t, 0, execResult.ExitCode)
			}
		})
	}
}

func TestE2E_AllTestData(t *testing.T) {
	requireServiceIntegrationTest(t)

	env := newServiceIntegrationEnv(t, 120*time.Second)
	compileOut := compileProgram(t, env, CompileRequest{
		Language:   model.LanguageCPP,
		SourceCode: readFixture(t, "cpp", "ac.cpp"),
	})
	assert.True(t, compileOut.Result.Succeeded)

	testCases := []struct {
		name       string
		inputFile  []string
		outputFile []string
	}{
		{name: "data1", inputFile: []string{"data1.in"}, outputFile: []string{"data1.out"}},
		{name: "data2", inputFile: []string{"data2.in"}, outputFile: []string{"data2.out"}},
		{name: "data3", inputFile: []string{"data3.in"}, outputFile: []string{"data3.out"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			execResult := env.runner.Execute(env.ctx, model.ExecuteRequest{
				ExecutablePath: compileOut.ArtifactPath,
				InputPath:      fixturePath(t, tc.inputFile...),
				Language:       model.LanguageCPP,
				TimeLimit:      1000,
				MemoryLimit:    128,
			})

			assert.Equal(t, model.VerdictOK, execResult.Verdict)
			assert.Equal(t, 0, execResult.ExitCode)
			assert.NotEmpty(t, execResult.Stdout, "Output should not be empty")
		})
	}
}

//nolint:funlen // Table-driven integration test with multiple language test cases
func TestE2E_WrongAnswerPrograms(t *testing.T) {
	requireServiceIntegrationTest(t)

	tests := []struct {
		name              string
		language          model.Language
		sourceFixture     []string
		inputFixture      []string
		timeLimit         int
		memoryLimit       int
		assertWrongOutput func(t *testing.T, stdout string)
	}{
		{
			name:          "C wrong answer",
			language:      model.LanguageC,
			sourceFixture: []string{"c", "wa.c"},
			inputFixture:  []string{"data1.in"},
			timeLimit:     1000,
			memoryLimit:   128,
			assertWrongOutput: func(t *testing.T, stdout string) {
				assert.NotContains(t, stdout, "0 1 1 1 1 1 1 4 4 5 8 9 9")
			},
		},
		{
			name:          "C++ wrong answer",
			language:      model.LanguageCPP,
			sourceFixture: []string{"cpp", "wa.cpp"},
			inputFixture:  []string{"data2.in"},
			timeLimit:     1000,
			memoryLimit:   128,
			assertWrongOutput: func(t *testing.T, stdout string) {
				assert.NotContains(t, stdout, "10 20 30 40 50")
			},
		},
		{
			name:          "Python wrong answer",
			language:      model.LanguagePython,
			sourceFixture: []string{"python", "wa.py"},
			inputFixture:  []string{"data3.in"},
			timeLimit:     1000,
			memoryLimit:   128,
			assertWrongOutput: func(t *testing.T, stdout string) {
				assert.NotEmpty(t, stdout)
			},
		},
		{
			name:          "Java wrong answer",
			language:      model.LanguageJava,
			sourceFixture: []string{"java", "wa", "Main.java"},
			inputFixture:  []string{"data2.in"},
			timeLimit:     2000,
			memoryLimit:   256,
			assertWrongOutput: func(t *testing.T, stdout string) {
				assert.NotContains(t, stdout, "10 20 30 40 50")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceIntegrationEnv(t, 90*time.Second)

			compileOut := compileProgram(t, env, CompileRequest{
				Language:   tt.language,
				SourceCode: readFixture(t, tt.sourceFixture...),
			})
			assert.True(t, compileOut.Result.Succeeded)

			execResult := env.runner.Execute(env.ctx, model.ExecuteRequest{
				ExecutablePath: compileOut.ArtifactPath,
				InputPath:      fixturePath(t, tt.inputFixture...),
				Language:       tt.language,
				TimeLimit:      tt.timeLimit,
				MemoryLimit:    tt.memoryLimit,
			})

			assert.Equal(t, model.VerdictOK, execResult.Verdict)
			assert.Equal(t, 0, execResult.ExitCode)
			tt.assertWrongOutput(t, execResult.Stdout)
		})
	}
}
