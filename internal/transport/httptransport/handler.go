package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"afterglow-judge-engine/internal/model"
)

// JudgeService is the judging capability required by the HTTP transport.
type JudgeService interface {
	Judge(context.Context, model.JudgeRequest) (model.JudgeResult, error)
}

type handler struct {
	judge   JudgeService
	logger  *slog.Logger
	maxSize int64 // max request body size in bytes
}

type errorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details string `json:"details,omitempty"`
}

func newHandler(judge JudgeService, logger *slog.Logger, maxSize int64) *handler {
	return &handler{
		judge:   judge,
		logger:  logger,
		maxSize: maxSize,
	}
}

func (h *handler) handleExecute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, h.maxSize)

	var req model.JudgeRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		h.writeInvalidRequest(w, err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.writeInvalidRequest(w, "request body must contain exactly one JSON object")
		return
	}

	result, err := h.judge.Judge(ctx, req)
	if err != nil {
		h.writeInvalidRequest(w, err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, result)
}

// writeJSON writes a JSON response.
func (h *handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("failed to encode response", "error", err)
	}
}

func (h *handler) writeInvalidRequest(w http.ResponseWriter, details string) {
	h.writeJSON(w, http.StatusBadRequest, errorResponse{
		Error:   http.StatusText(http.StatusBadRequest),
		Code:    "INVALID_REQUEST",
		Details: details,
	})
}
