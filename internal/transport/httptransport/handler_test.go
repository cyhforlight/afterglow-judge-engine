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

func makeJudgeBody(t *testing.T, req model.JudgeRequest) io.Reader {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)
	return bytes.NewReader(body)
}

func validJudgeRequest() model.JudgeRequest {
	return model.JudgeRequest{
		SourceCode:  "print(42)",
		Checker:     "default",
		Language:    model.LanguagePython,
		TimeLimit:   1000,
		MemoryLimit: 128,
		TestCases: []model.JudgeTestCase{
			{InputText: "", ExpectedOutput: "42\n"},
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

	var resp struct {
		Status string `json:"status"`
		Cases  []struct {
			Verdict string `json:"verdict"`
		} `json:"cases"`
		PassedCount int `json:"passedCount"`
	}
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "OK", resp.Status)
	require.Len(t, resp.Cases, 1)
	assert.Equal(t, "OK", resp.Cases[0].Verdict)
	assert.Equal(t, 1, resp.PassedCount)
	assert.Equal(t, "Python", judge.lastRequest.Language.String())
	assert.Equal(t, "default", judge.lastRequest.Checker)
	assert.Equal(t, "42\n", judge.lastRequest.TestCases[0].ExpectedOutput)
}

func TestHandleExecute_RejectsMalformedBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: "invalid"},
		{name: "unknown field", body: `{"sourceCode":"x","language":"Python","timeLimit":1,"memoryLimit":1,"testcases":[{"name":"c"}],"unknown":1}`},
		{name: "invalid language", body: `{"sourceCode":"x","language":"Ruby","timeLimit":1,"memoryLimit":1,"testcases":[{}]}`},
	}

	handler := newTestHandler(&mockJudgeService{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()
			handler.HandleExecute(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
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

	var resp errorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, `checker "ncmp" is not allowed`, resp.Details)
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
