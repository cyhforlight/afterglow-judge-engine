package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"afterglow-judge-engine/internal/execution"
	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/resource"
	"afterglow-judge-engine/internal/sandbox"
	"afterglow-judge-engine/internal/service"

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
	filename      string
	language      model.Language
	overallStatus string
	caseVerdicts  []e2eVerdictExpectation
}

type e2eVerdictExpectation struct {
	name    string
	allowed []string
}

type judgeHTTPResponse struct {
	Status string                  `json:"status"`
	Cases  []judgeCaseHTTPResponse `json:"cases"`
}

type judgeCaseHTTPResponse struct {
	Verdict string `json:"verdict"`
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
				filename:      "code_1_ac.cpp",
				language:      model.LanguageCPP,
				overallStatus: "OK",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"OK"}},
					{name: "sum3", allowed: []string{"OK"}},
					{name: "sum4", allowed: []string{"OK"}},
					{name: "sum5", allowed: []string{"OK"}},
				},
			},
			{
				filename:      "code_2_tle.cpp",
				language:      model.LanguageCPP,
				overallStatus: "OK",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"OK"}},
					{name: "sum3", allowed: []string{"OK"}},
					{name: "sum4", allowed: []string{"OK"}},
					{name: "sum5", allowed: []string{"TimeLimitExceeded"}},
				},
			},
			{
				filename:      "code_3_wa_and_tle.cpp",
				language:      model.LanguageCPP,
				overallStatus: "OK",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"OK"}},
					{name: "sum3", allowed: []string{"OK"}},
					{name: "sum4", allowed: []string{"WrongAnswer"}},
					{name: "sum5", allowed: []string{"TimeLimitExceeded"}},
				},
			},
			{
				filename:      "code_4_wa_and_tle.py",
				language:      model.LanguagePython,
				overallStatus: "OK",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"OK"}},
					{name: "sum3", allowed: []string{"WrongAnswer"}},
					{name: "sum4", allowed: []string{"WrongAnswer"}},
					{name: "sum5", allowed: []string{"TimeLimitExceeded"}},
				},
			},
			{
				filename:      "code_5_wa_and_tle.c",
				language:      model.LanguageC,
				overallStatus: "OK",
				caseVerdicts: []e2eVerdictExpectation{
					{name: "sum1", allowed: []string{"OK"}},
					{name: "sum2", allowed: []string{"WrongAnswer"}},
					{name: "sum3", allowed: []string{"WrongAnswer", "TimeLimitExceeded"}},
					{name: "sum4", allowed: []string{"TimeLimitExceeded"}},
					{name: "sum5", allowed: []string{"TimeLimitExceeded"}},
				},
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

	sb, err := sandbox.New("/run/containerd/containerd.sock", "afterglow-e2e")
	require.NoError(t, err)
	bundledFS, err := resource.NewBundled()
	require.NoError(t, err)

	testdataDir := filepath.Join(projectRoot(t), "testdata")
	externalFS, err := resource.NewExternal(testdataDir)
	require.NoError(t, err)

	executor, err := execution.NewExecutor(sb, 8)
	require.NoError(t, err)
	judge, err := service.NewJudgeEngine(executor, bundledFS, externalFS, 10, model.DefaultJudgeLimits())
	require.NoError(t, err)

	ctx := context.Background()
	if err := sb.CheckEnvironment(ctx); err != nil {
		t.Skipf("sandbox environment unavailable: %v", err)
	}

	return newHandler(judge, slog.Default(), 256*testBytesPerMiB)
}

func executeJudgeRequest(t *testing.T, handler *Handler, reqBody model.JudgeRequest) judgeHTTPResponse {
	t.Helper()

	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp judgeHTTPResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	return resp
}

func TestE2E_HTTP_ExternalCases(t *testing.T) {
	requireE2EPrerequisites(t)
	handler := newE2EHandler(t)

	for _, suite := range e2eProblemSuites {
		t.Run(suite.name, func(t *testing.T) {
			testCases := loadProblemTestCases(t, suite.dir)

			for _, codeExpectation := range suite.codes {
				t.Run(codeExpectation.filename, func(t *testing.T) {
					t.Parallel()
					reqBody := model.JudgeRequest{
						SourceCode:  readSourceCode(t, suite.dir, codeExpectation.filename),
						Checker:     suite.checker,
						Language:    codeExpectation.language,
						TimeLimit:   suite.timeLimit,
						MemoryLimit: suite.memoryLimit,
						TestCases:   testCases,
					}

					resp := executeJudgeRequest(t, handler, reqBody)

					assert.Equal(t, codeExpectation.overallStatus, resp.Status)
					require.Len(t, resp.Cases, len(codeExpectation.caseVerdicts))
					assertCaseVerdicts(t, codeExpectation.caseVerdicts, resp.Cases)
				})
			}
		})
	}
}

func loadProblemTestCases(t *testing.T, problemDir string) []model.JudgeTestCase {
	t.Helper()

	pattern := filepath.Join(projectRoot(t), "testdata", problemDir, "data", "*.in")
	inputFiles, err := filepath.Glob(pattern)
	require.NoError(t, err)
	require.NotEmpty(t, inputFiles, "no input files found for %s", problemDir)
	slices.Sort(inputFiles)

	testCases := make([]model.JudgeTestCase, 0, len(inputFiles))
	for _, inputPath := range inputFiles {
		name := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		outputPath := strings.TrimSuffix(inputPath, ".in") + ".out"

		if _, err := os.Stat(outputPath); err != nil {
			t.Fatalf("missing output file for %s: %v", inputPath, err)
		}

		testCases = append(testCases, model.JudgeTestCase{
			InputFile:          filepath.ToSlash(filepath.Join(problemDir, "data", name+".in")),
			ExpectedOutputFile: filepath.ToSlash(filepath.Join(problemDir, "data", name+".out")),
		})
	}

	return testCases
}

func readSourceCode(t *testing.T, problemDir, filename string) string {
	t.Helper()

	path := filepath.Join(projectRoot(t), "testdata", problemDir, "code", filename)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(content)
}

func assertCaseVerdicts(t *testing.T, expected []e2eVerdictExpectation, actual []judgeCaseHTTPResponse) {
	t.Helper()

	for i, caseResult := range actual {
		assert.Contains(t, expected[i].allowed, caseResult.Verdict,
			"case %d verdict mismatch", i)
	}
}
