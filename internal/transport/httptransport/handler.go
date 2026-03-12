package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"afterglow-judge-engine/internal/service"
)

// Handler handles HTTP requests for judging.
type Handler struct {
	judge   service.JudgeService
	logger  *slog.Logger
	maxSize int64 // max request body size in bytes
}

// NewHandler creates a new HTTP handler.
func NewHandler(judge service.JudgeService, logger *slog.Logger, maxSizeMB int) *Handler {
	return &Handler{
		judge:   judge,
		logger:  logger,
		maxSize: int64(maxSizeMB) * 1024 * 1024,
	}
}

// HandleExecute handles POST /v1/execute requests.
func (h *Handler) HandleExecute(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, h.maxSize)

	var req JudgeRequestDTO
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

	if err := req.Validate(); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	judgeRequest, err := req.ToModel()
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	if err := h.judge.ValidateRequest(ctx, judgeRequest); err != nil {
		h.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	result := h.judge.Judge(ctx, judgeRequest)
	h.writeJSON(w, http.StatusOK, ToJudgeResponse(result))
}

// HandleHealth handles GET /health requests.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.judge.PreflightCheck(ctx); err != nil {
		h.writeError(w, http.StatusServiceUnavailable, "UNHEALTHY", err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{"status": "healthy"})
}

// writeJSON writes a JSON response.
func (h *Handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.logger.Error("failed to encode response", "error", err)
	}
}

// writeError writes an error response.
func (h *Handler) writeError(w http.ResponseWriter, status int, code, details string) {
	writeErrorResponse(w, h.logger, status, code, details)
}
