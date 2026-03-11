package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"afterglow-judge-sandbox/internal/cache"
	"afterglow-judge-sandbox/internal/sandbox"
	"afterglow-judge-sandbox/internal/service"
	"afterglow-judge-sandbox/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	internalStorage, err := storage.NewInternalStorage(filepath.Join(projectRoot(t), "support"))
	require.NoError(t, err)
	compileCache, err := cache.New(100)
	require.NoError(t, err)

	// Initialize ExternalStorage for test data files
	testdataDir := filepath.Join(projectRoot(t), "testdata")
	externalStorage, err := storage.NewExternalStorage(testdataDir, compileCache)
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

func TestE2E_HTTP_Python_OK(t *testing.T) {
	requireE2EPrerequisites(t)
	handler := newE2EHandler(t)

	reqBody := JudgeRequestDTO{
		SourceCode: `import sys
n = int(sys.stdin.readline())
print(n * 2)`,
		Language:    "Python",
		TimeLimit:   2000,
		MemoryLimit: 256,
		TestCases: []JudgeTestCaseDTO{
			{Name: "case-1", InputText: "21\n", ExpectedOutputText: "42\n"},
			{Name: "case-2", InputText: "7\n", ExpectedOutputText: "14\n"},
		},
	}

	resp := executeJudgeRequest(t, handler, reqBody)

	assert.Equal(t, "OK", resp.Verdict)
	assert.True(t, resp.Compile.Succeeded)
	assert.Equal(t, 2, resp.PassedCount)
	assert.Equal(t, 2, resp.TotalCount)
}

func TestE2E_HTTP_Python_WA(t *testing.T) {
	requireE2EPrerequisites(t)
	handler := newE2EHandler(t)

	reqBody := JudgeRequestDTO{
		SourceCode:  `print("41")`,
		Language:    "Python",
		TimeLimit:   2000,
		MemoryLimit: 256,
		TestCases:   []JudgeTestCaseDTO{{Name: "case-1", InputText: "", ExpectedOutputText: "42\n"}},
	}

	resp := executeJudgeRequest(t, handler, reqBody)

	assert.Equal(t, "WrongAnswer", resp.Verdict)
	require.Len(t, resp.Cases, 1)
	assert.Equal(t, "WrongAnswer", resp.Cases[0].Verdict)
}

func TestE2E_HTTP_Python_CustomChecker(t *testing.T) {
	requireE2EPrerequisites(t)
	handler := newE2EHandler(t)

	reqBody := JudgeRequestDTO{
		SourceCode:  `print("yes")`,
		Checker:     "yesno",
		Language:    "Python",
		TimeLimit:   2000,
		MemoryLimit: 256,
		TestCases:   []JudgeTestCaseDTO{{Name: "case-1", InputText: "", ExpectedOutputText: "YES\n"}},
	}

	resp := executeJudgeRequest(t, handler, reqBody)

	assert.Equal(t, "OK", resp.Verdict)
	require.Len(t, resp.Cases, 1)
	assert.Equal(t, "OK", resp.Cases[0].Verdict)
}

func TestE2E_HTTP_CPP_TLE(t *testing.T) {
	requireE2EPrerequisites(t)
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ not available")
	}

	handler := newE2EHandler(t)

	reqBody := JudgeRequestDTO{
		SourceCode: `int main() {
  while (true) {}
  return 0;
}`,
		Language:    "C++",
		TimeLimit:   1000,
		MemoryLimit: 256,
		TestCases:   []JudgeTestCaseDTO{{Name: "case-1", InputText: "", ExpectedOutputText: ""}},
	}

	resp := executeJudgeRequest(t, handler, reqBody)
	assert.Equal(t, "TimeLimitExceeded", resp.Verdict)
}

func TestE2E_HTTP_ConcurrentJudges(t *testing.T) {
	requireE2EPrerequisites(t)
	handler := newE2EHandler(t)

	const numRequests = 3
	results := make(chan string, numRequests)

	for range numRequests {
		go func() {
			reqBody := JudgeRequestDTO{
				SourceCode:  `print("OK")`,
				Language:    "Python",
				TimeLimit:   2000,
				MemoryLimit: 256,
				TestCases:   []JudgeTestCaseDTO{{Name: "case-1", InputText: "", ExpectedOutputText: "OK\n"}},
			}
			resp, err := executeConcurrentJudgeRequest(handler, reqBody)
			if err != nil {
				results <- "REQUEST_ERROR"
				return
			}
			if resp.Verdict != "OK" && len(resp.Cases) > 0 {
				t.Logf("Unexpected verdict: %s, stdout: %s, extraInfo: %s",
					resp.Verdict, resp.Cases[0].Stdout, resp.Cases[0].ExtraInfo)
			}
			results <- resp.Verdict
		}()
	}

	for range numRequests {
		select {
		case verdict := <-results:
			assert.Equal(t, "OK", verdict)
		case <-time.After(30 * time.Second):
			t.Fatal("timeout waiting for concurrent execution")
		}
	}
}

func executeConcurrentJudgeRequest(handler *Handler, reqBody JudgeRequestDTO) (JudgeResponseDTO, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return JudgeResponseDTO{}, err
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	return decodeJudgeResponse(w.Body)
}

func TestE2E_HTTP_ExternalTestFiles(t *testing.T) {
	requireE2EPrerequisites(t)
	handler := newE2EHandler(t)

	// Read actual code from P1 test case
	codeBytes, err := os.ReadFile(filepath.Join(projectRoot(t),
		"testdata/E2E_cases/P1/code/code_1_ac.cpp"))
	require.NoError(t, err)

	reqBody := JudgeRequestDTO{
		SourceCode:  string(codeBytes),
		Language:    "C++",
		TimeLimit:   1000,
		MemoryLimit: 256,
		TestCases: []JudgeTestCaseDTO{
			{
				Name:               "sum1",
				InputFile:          "E2E_cases/P1/data/sum1.in",
				ExpectedOutputFile: "E2E_cases/P1/data/sum1.out",
			},
			{
				Name:               "sum2",
				InputFile:          "E2E_cases/P1/data/sum2.in",
				ExpectedOutputFile: "E2E_cases/P1/data/sum2.out",
			},
		},
	}

	resp := executeJudgeRequest(t, handler, reqBody)

	assert.Equal(t, "OK", resp.Verdict)
	assert.True(t, resp.Compile.Succeeded)
	assert.Equal(t, 2, resp.PassedCount)
	assert.Equal(t, 2, resp.TotalCount)
}

func TestE2E_HTTP_FileNotFound(t *testing.T) {
	requireE2EPrerequisites(t)
	handler := newE2EHandler(t)

	reqBody := JudgeRequestDTO{
		SourceCode:  `print("test")`,
		Language:    "Python",
		TimeLimit:   2000,
		MemoryLimit: 256,
		TestCases: []JudgeTestCaseDTO{
			{
				Name:               "missing",
				InputFile:          "nonexistent/file.in",
				ExpectedOutputFile: "E2E_cases/P1/data/sum1.out",
			},
		},
	}

	body, err := json.Marshal(reqBody)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	resp, err := decodeJudgeResponse(w.Body)
	require.NoError(t, err)

	assert.Equal(t, "UnknownError", resp.Verdict)
	assert.Contains(t, resp.Compile.Log, "test data loading failed")
	assert.Contains(t, resp.Compile.Log, "nonexistent/file.in")
}
