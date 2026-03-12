package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"afterglow-judge-engine/internal/cache"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/service"
	"afterglow-judge-engine/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type e2eProblemSuite struct {
	name        string
	dir         string
	checker     string
	timeLimit   int
	memoryLimit int
	codes       []e2eCodeExpectation
}

type e2eCodeExpectation struct {
	filename       string
	language       string
	overallVerdict string
	caseVerdicts   []e2eVerdictExpectation
	passedCount    int
}

type e2eVerdictExpectation struct {
	name    string
	allowed []string
}

var e2eProblemSuites = []e2eProblemSuite{
	{
		name:        "P1",
		dir:         "E2E_cases/P1",
		checker:     "ncmp",
		timeLimit:   1000,
		memoryLimit: 256,
		codes: []e2eCodeExpectation{
			{
				filename:       "code_1_ac.cpp",
				language:       "C++",
				overallVerdict: "OK",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"OK"}},
					{name: "sum3", allowed: []string{"OK"}},
					{name: "sum4", allowed: []string{"OK"}},
					{name: "sum5", allowed: []string{"OK"}},
				},
				passedCount: 5,
			},
			{
				filename:       "code_2_tle.cpp",
				language:       "C++",
				overallVerdict: "TimeLimitExceeded",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"OK"}},
					{name: "sum3", allowed: []string{"OK"}},
					{name: "sum4", allowed: []string{"OK"}},
					{name: "sum5", allowed: []string{"TimeLimitExceeded"}},
				},
				passedCount: 4,
			},
			{
				filename:       "code_3_wa_and_tle.cpp",
				language:       "C++",
				overallVerdict: "TimeLimitExceeded",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"OK"}},
					{name: "sum3", allowed: []string{"OK"}},
					{name: "sum4", allowed: []string{"WrongAnswer"}},
					{name: "sum5", allowed: []string{"TimeLimitExceeded"}},
				},
				passedCount: 3,
			},
			{
				filename:       "code_4_wa_and_tle.py",
				language:       "Python",
				overallVerdict: "TimeLimitExceeded",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"OK"}},
					{name: "sum3", allowed: []string{"WrongAnswer"}},
					{name: "sum4", allowed: []string{"WrongAnswer"}},
					{name: "sum5", allowed: []string{"TimeLimitExceeded"}},
				},
				passedCount: 2,
			},
			{
				filename:       "code_5_wa_and_tle.c",
				language:       "C",
				overallVerdict: "TimeLimitExceeded",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"WrongAnswer"}},
					{name: "sum3", allowed: []string{"WrongAnswer", "TimeLimitExceeded"}},
					{name: "sum4", allowed: []string{"TimeLimitExceeded"}},
					{name: "sum5", allowed: []string{"TimeLimitExceeded"}},
				},
				passedCount: 1,
			},
		},
	},
}

func requireE2EPrerequisites(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("E2E tests require root privileges")
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("failed to locate project root")
		}
		dir = parent
	}
}

func newE2EHandler(t *testing.T) *Handler {
	t.Helper()

	sb := sandbox.NewContainerdSandbox("/run/containerd/containerd.sock", "")
	internalStorage, err := storage.NewBundledInternalStorage()
	require.NoError(t, err)
	compileCache, err := cache.New(100)
	require.NoError(t, err)

	testdataDir := filepath.Join(projectRoot(t), "testdata")
	externalStorage, err := storage.NewExternalStorage(testdataDir)
	require.NoError(t, err)

	baseCompiler := service.NewCompiler(sb)
	baseRunner := service.NewRunner(sb)
	judge, err := service.NewJudgeEngine(baseCompiler, baseRunner,
		internalStorage, externalStorage, "default", compileCache)
	require.NoError(t, err)

	ctx := context.Background()
	if err := judge.PreflightCheck(ctx); err != nil {
		t.Skipf("Containerd not available: %v", err)
	}

	return NewHandler(judge, slog.Default(), 256)
}

func decodeJudgeResponse(body *bytes.Buffer) (JudgeResponseDTO, error) {
	var resp JudgeResponseDTO
	err := json.NewDecoder(body).Decode(&resp)
	return resp, err
}

func executeJudgeRequest(t *testing.T, handler *Handler, reqBody JudgeRequestDTO) JudgeResponseDTO {
	t.Helper()

	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	resp, err := decodeJudgeResponse(w.Body)
	require.NoError(t, err)
	return resp
}

func TestE2E_HTTP_ExternalCases(t *testing.T) {
	requireE2EPrerequisites(t)
	handler := newE2EHandler(t)

	for _, suite := range e2eProblemSuites {
		t.Run(suite.name, func(t *testing.T) {
			testCases := loadProblemTestCases(t, suite.dir)
			assertSuiteMatchesCodeDirectory(t, suite)

			for _, codeExpectation := range suite.codes {
				t.Run(codeExpectation.filename, func(t *testing.T) {
					reqBody := JudgeRequestDTO{
						SourceCode:  readSourceCode(t, suite.dir, codeExpectation.filename),
						Checker:     suite.checker,
						Language:    codeExpectation.language,
						TimeLimit:   suite.timeLimit,
						MemoryLimit: suite.memoryLimit,
						TestCases:   testCases,
					}

					resp := executeJudgeRequest(t, handler, reqBody)

					assert.Equal(t, codeExpectation.overallVerdict, resp.Verdict)
					assert.Equal(t, codeExpectation.passedCount, resp.PassedCount)
					assert.Equal(t, len(testCases), resp.TotalCount)
					require.Len(t, resp.Cases, len(codeExpectation.caseVerdicts))
					assertCaseVerdicts(t, codeExpectation.caseVerdicts, resp.Cases)
				})
			}
		})
	}
}

func loadProblemTestCases(t *testing.T, problemDir string) []JudgeTestCaseDTO {
	t.Helper()

	pattern := filepath.Join(projectRoot(t), "testdata", problemDir, "data", "*.in")
	inputFiles, err := filepath.Glob(pattern)
	require.NoError(t, err)
	require.NotEmpty(t, inputFiles, "no input files found for %s", problemDir)
	slices.Sort(inputFiles)

	testCases := make([]JudgeTestCaseDTO, 0, len(inputFiles))
	for _, inputPath := range inputFiles {
		name := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		outputPath := strings.TrimSuffix(inputPath, ".in") + ".out"

		if _, err := os.Stat(outputPath); err != nil {
			t.Fatalf("missing output file for %s: %v", inputPath, err)
		}

		testCases = append(testCases, JudgeTestCaseDTO{
			InputFile:          filepath.ToSlash(filepath.Join(problemDir, "data", name+".in")),
			ExpectedOutputFile: filepath.ToSlash(filepath.Join(problemDir, "data", name+".out")),
		})
	}

	return testCases
}

func assertSuiteMatchesCodeDirectory(t *testing.T, suite e2eProblemSuite) {
	t.Helper()

	pattern := filepath.Join(projectRoot(t), "testdata", suite.dir, "code", "*")
	codeFiles, err := filepath.Glob(pattern)
	require.NoError(t, err)
	require.NotEmpty(t, codeFiles, "no code files found for %s", suite.name)

	discovered := make([]string, 0, len(codeFiles))
	for _, codeFile := range codeFiles {
		info, err := os.Stat(codeFile)
		require.NoError(t, err)
		if info.IsDir() {
			continue
		}
		discovered = append(discovered, filepath.Base(codeFile))
	}
	slices.Sort(discovered)

	configured := make([]string, 0, len(suite.codes))
	for _, codeExpectation := range suite.codes {
		configured = append(configured, codeExpectation.filename)
	}
	slices.Sort(configured)

	assert.Equal(t, configured, discovered,
		fmt.Sprintf("suite %s must cover all and only code files in testdata", suite.name))
}

func readSourceCode(t *testing.T, problemDir, filename string) string {
	t.Helper()

	path := filepath.Join(projectRoot(t), "testdata", problemDir, "code", filename)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

func assertCaseVerdicts(t *testing.T, expected []e2eVerdictExpectation, actual []JudgeCaseResultDTO) {
	t.Helper()

	for i, caseResult := range actual {
		assert.Contains(t, expected[i].allowed, caseResult.Verdict,
			"case %d verdict mismatch", i)
	}
}
