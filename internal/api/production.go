package api

import (
	"fmt"
	"net/http"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

const (
	maxPalletsPerBatch = 10
	maxBatchCodeLen    = 64
)

func (h *handler) createBatch(w http.ResponseWriter, r *http.Request) {
	var in app.CreateBatchInput
	if err := decodeBody(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	if in.BatchCode == "" || len(in.BatchCode) > maxBatchCodeLen {
		writeError(w, r, fmt.Errorf("%w: batch_code is required (at most %d characters)", domain.ErrInvalidInput, maxBatchCodeLen))
		return
	}
	if in.Pallets < 1 || in.Pallets > maxPalletsPerBatch {
		writeError(w, r, fmt.Errorf("%w: pallets must be between 1 and %d", domain.ErrInvalidInput, maxPalletsPerBatch))
		return
	}
	view, err := h.svc.CreateBatch(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (h *handler) traceSerial(w http.ResponseWriter, r *http.Request) {
	code, err := domain.ParseSerialCode(r.PathValue("code"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	trace, err := h.svc.TraceSerial(r.Context(), code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

func (h *handler) quarantine(w http.ResponseWriter, r *http.Request) {
	code, err := domain.ParseSerialCode(r.PathValue("code"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	trace, err := h.svc.Quarantine(r.Context(), code, in.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}

func (h *handler) releaseQuarantine(w http.ResponseWriter, r *http.Request) {
	code, err := domain.ParseSerialCode(r.PathValue("code"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	trace, err := h.svc.ReleaseQuarantine(r.Context(), code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trace)
}
