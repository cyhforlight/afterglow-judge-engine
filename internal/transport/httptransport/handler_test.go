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
	err        error
	judgeCalls int
}

func (m *mockJudgeService) Judge(_ context.Context, _ model.JudgeRequest) (model.JudgeResult, error) {
	m.judgeCalls++
	return model.JudgeResult{}, m.err
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

func newTestHandler(judge JudgeService) *handler {
	return newHandler(judge, slog.Default(), maxRequestBodyBytes)
}

func TestHandleExecute_RejectsMalformedBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "two JSON objects", body: "{}{}"},
		{name: "unknown field", body: `{"sourceCode":"x","language":"Python","timeLimit":1,"memoryLimit":1,"testcases":[{"name":"c"}],"unknown":1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			judge := &mockJudgeService{}
			handler := newTestHandler(judge)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/execute", bytes.NewBufferString(tt.body))
			w := httptest.NewRecorder()
			handler.handleExecute(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Zero(t, judge.judgeCalls)

			var resp errorResponse
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, "INVALID_REQUEST", resp.Code)
			assert.NotEmpty(t, resp.Details)
		})
	}
}

func TestHandleExecute_ServiceRejectionReturnsBadRequest(t *testing.T) {
	judge := &mockJudgeService{err: errors.New(`inputFile "cases/1.in" is not available`)}
	handler := newTestHandler(judge)

	dto := validJudgeRequest()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.handleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.Equal(t, 1, judge.judgeCalls)

	var resp errorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, http.StatusText(http.StatusBadRequest), resp.Error)
	assert.Equal(t, "INVALID_REQUEST", resp.Code)
	assert.Equal(t, `inputFile "cases/1.in" is not available`, resp.Details)
}

func TestHandleExecute_BodyTooLarge(t *testing.T) {
	judge := &mockJudgeService{}
	handler := newHandler(judge, slog.Default(), 1)

	dto := validJudgeRequest()
	dto.SourceCode = "abcdefghijklmnopqrstuvwxyz"

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/execute", makeJudgeBody(t, dto))
	w := httptest.NewRecorder()
	handler.handleExecute(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Zero(t, judge.judgeCalls)

	var resp errorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "INVALID_REQUEST", resp.Code)
	assert.Contains(t, resp.Details, "request body too large")
}
