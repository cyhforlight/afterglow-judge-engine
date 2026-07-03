package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"afterglow-judge-engine/internal/model"
	"afterglow-judge-engine/internal/service"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockJudgeService struct {
	preflightErr error
	validateErr  error
	result       model.JudgeResult
	lastRequest  model.JudgeRequest
	judgeCalls   int
}

func (m *mockJudgeService) PreflightCheck(_ context.Context) error {
	return m.preflightErr
}

func (m *mockJudgeService) ValidateRequest(_ context.Context, req model.JudgeRequest) error {
	m.lastRequest = req
	return m.validateErr
}

func (m *mockJudgeService) Judge(_ context.Context, req model.JudgeRequest) model.JudgeResult {
	m.judgeCalls++
	m.lastRequest = req
	return m.result
}

func makeJudgeBody(t *testing.T, dto JudgeRequestDTO) io.Reader {
	t.Helper()
	body, err := json.Marshal(dto)
	require.NoError(t, err)
	return bytes.NewReader(body)
}

func validJudgeRequest() JudgeRequestDTO {
	return JudgeRequestDTO{
		SourceCode:  "print(42)",
		Checker:     "default",
		Language:    "Python",
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []JudgeTestCaseDTO{
			{InputText: "", ExpectedOutputText: "42\n"},
		},
	}
}

func newTestHandler(judge service.JudgeService) *Handler {
	return newTestHandlerWithSize(judge, 256)
}

func newTestHandlerWithSize(judge service.JudgeService, maxSizeMB int) *Handler {
	return NewHandler(judge, slog.Default(), maxSizeMB)
}

func TestHandleHealth_Success(t *testing.T) {
	handler := newTestHandler(&mockJudgeService{})

	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	w := httptest.NewRecorder()
	handler.HandleHealth(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "healthy")
}

func TestHandleHealth_Unhealthy(t *testing.T) {
	handler := newTestHandler(&mockJudgeService{preflightErr: errors.New("down")})

	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	w := httptest.NewRecorder()
	handler.HandleHealth(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHandleExecute_Success(t *testing.T) {
	judge := &mockJudgeService{result: model.JudgeResult{
		Status:  model.JudgeStatusOK,
		Compile: model.CompileResult{Succeeded: true, Log: "ok"},
		Cases: []model.JudgeCaseResult{{
			Verdict:   model.VerdictOK,
			Stdout:    "42\n",
			ExitCode:  0,
			ExtraInfo: "",
		}},
		PassedCount: 1,
		TotalCount:  1,
	}}
	handler := newTestHandler(judge)

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, validJudgeRequest()))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp JudgeResponseDTO
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "OK", resp.Status)
	assert.Equal(t, 1, resp.PassedCount)
	assert.Equal(t, "Python", judge.lastRequest.Language.String())
	assert.Equal(t, "default", judge.lastRequest.Checker)
}

func TestHandleExecute_InvalidJSON(t *testing.T) {
	handler := newTestHandler(&mockJudgeService{})

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader([]byte("invalid")))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleExecute_UnknownField(t *testing.T) {
	handler := newTestHandler(&mockJudgeService{})

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader([]byte(`{"sourceCode":"x","language":"Python","timeLimit":1,"memoryLimit":1,"testcases":[{"name":"c"}],"unknown":1}`)))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleExecute_MissingFields(t *testing.T) {
	handler := newTestHandler(&mockJudgeService{validateErr: errors.New("sourceCode is required")})

	dto := validJudgeRequest()
	dto.SourceCode = ""

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleExecute_InvalidLanguage(t *testing.T) {
	handler := newTestHandler(&mockJudgeService{})

	dto := validJudgeRequest()
	dto.Language = "Ruby"

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleExecute_RequestLimitExceeded(t *testing.T) {
	judge := &mockJudgeService{validateErr: errors.New("timeLimit must be at most 999 ms")}
	handler := NewHandler(judge, slog.Default(), 256)

	dto := validJudgeRequest()
	dto.TimeLimit = 1000

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, judge.judgeCalls)
	assert.Equal(t, "print(42)", judge.lastRequest.SourceCode)
}

func TestHandleExecute_InvalidChecker(t *testing.T) {
	judge := &mockJudgeService{validateErr: errors.New(`checker "ncmp" is not allowed`)}
	handler := newTestHandler(judge)

	dto := validJudgeRequest()
	dto.Checker = "ncmp"

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, judge.judgeCalls)

	var resp ErrorResponseDTO
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, `checker "ncmp" is not allowed`, resp.Details)
}

func TestHandleExecute_MissingExternalDependency(t *testing.T) {
	judge := &mockJudgeService{
		validateErr: errors.New(`testcases[0]: inputFile "cases/1.in" requires external resources`),
	}
	handler := newTestHandler(judge)

	dto := validJudgeRequest()
	dto.TestCases = []JudgeTestCaseDTO{{
		InputFile:          "cases/1.in",
		ExpectedOutputText: "42\n",
	}}

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp ErrorResponseDTO
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "INVALID_REQUEST", resp.Code)
	assert.Contains(t, resp.Details, `inputFile "cases/1.in" requires external resources`)
}

func TestHandleExecute_BodyTooLarge(t *testing.T) {
	handler := newTestHandlerWithSize(&mockJudgeService{}, 0)

	dto := validJudgeRequest()
	dto.SourceCode = "abcdefghijklmnopqrstuvwxyz"

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
