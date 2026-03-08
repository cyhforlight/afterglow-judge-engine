package service

import (
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceIntegrationEnv(t, 90*time.Second)

			sourceCode := tt.sourceText
			if len(tt.sourceFixture) > 0 {
				sourceCode = readFixture(t, tt.sourceFixture...)
			}

			compileOut := compileProgram(t, env, UserCodeCompileRequest{
				Language:   tt.language,
				SourceCode: sourceCode,
			})
			assert.True(t, compileOut.Result.Succeeded)

			inputText := tt.inputText
			if len(tt.inputFixture) > 0 {
				inputText = readFixture(t, tt.inputFixture...)
			}

			execResult, err := env.runner.Execute(env.ctx, model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       inputText,
				Language:    tt.language,
				TimeLimit:   tt.timeLimit,
				MemoryLimit: tt.memoryLimit,
			})
			require.NoError(t, err)

			assert.Equal(t, model.VerdictOK, execResult.Verdict)
			assert.Equal(t, 0, execResult.ExitCode)
			tt.assertOutput(t, execResult.Stdout)
		})
	}
}

func TestE2E_AllTestData(t *testing.T) {
	requireServiceIntegrationTest(t)

	env := newServiceIntegrationEnv(t, 120*time.Second)
	compileOut := compileProgram(t, env, UserCodeCompileRequest{
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
			execResult, err := env.runner.Execute(env.ctx, model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       readFixture(t, tc.inputFile...),
				Language:    model.LanguageCPP,
				TimeLimit:   1000,
				MemoryLimit: 128,
			})
			require.NoError(t, err)

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newServiceIntegrationEnv(t, 90*time.Second)

			compileOut := compileProgram(t, env, UserCodeCompileRequest{
				Language:   tt.language,
				SourceCode: readFixture(t, tt.sourceFixture...),
			})
			assert.True(t, compileOut.Result.Succeeded)

			execResult, err := env.runner.Execute(env.ctx, model.ExecuteRequest{
				Program:     *compileOut.Artifact,
				Input:       readFixture(t, tt.inputFixture...),
				Language:    tt.language,
				TimeLimit:   tt.timeLimit,
				MemoryLimit: tt.memoryLimit,
			})
			require.NoError(t, err)

			assert.Equal(t, model.VerdictOK, execResult.Verdict)
			assert.Equal(t, 0, execResult.ExitCode)
			tt.assertWrongOutput(t, execResult.Stdout)
		})
	}
}

func TestE2E_CompileError(t *testing.T) {
	requireServiceIntegrationTest(t)

	env := newServiceIntegrationEnv(t, 30*time.Second)

	compileOut := compileProgram(t, env, UserCodeCompileRequest{
		Language:   model.LanguageC,
		SourceCode: "int main() { int x = 10 return 0; }", // Missing semicolon
	})

	assert.False(t, compileOut.Result.Succeeded)
	assert.Contains(t, compileOut.Result.Log, "error")
}
