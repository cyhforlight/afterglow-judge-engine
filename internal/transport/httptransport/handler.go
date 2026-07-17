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
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body must contain exactly one JSON object")
		return
	}

	result, err := h.judge.Judge(ctx, req)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
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

// writeError writes an error response.
func (h *handler) writeError(w http.ResponseWriter, status int, code, details string) {
	writeErrorResponse(w, h.logger, status, code, details)
}
