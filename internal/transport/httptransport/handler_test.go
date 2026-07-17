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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockJudgeService struct {
	err         error
	result      model.JudgeResult
	lastRequest model.JudgeRequest
	judgeCalls  int
}

const testBytesPerMiB = int64(1024 * 1024)

func (m *mockJudgeService) Judge(_ context.Context, req model.JudgeRequest) (model.JudgeResult, error) {
	m.judgeCalls++
	m.lastRequest = req
	return m.result, m.err
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

func newTestHandler(judge JudgeService) *Handler {
	return newTestHandlerWithSize(judge, 256)
}

func newTestHandlerWithSize(judge JudgeService, maxSizeMB int) *Handler {
	return newHandler(judge, slog.Default(), int64(maxSizeMB)*testBytesPerMiB)
}

func TestHandleExecute_RejectsMalformedBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: "invalid"},
		{name: "unknown field", body: `{"sourceCode":"x","language":"Python","timeLimit":1,"memoryLimit":1,"testcases":[{"name":"c"}],"unknown":1}`},
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
	judge := &mockJudgeService{err: errors.New(`checker "ncmp" is not allowed`)}
	handler := newTestHandler(judge)

	dto := validJudgeRequest()
	dto.Checker = "ncmp"

	req := httptest.NewRequest(http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.HandleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 1, judge.judgeCalls)

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
