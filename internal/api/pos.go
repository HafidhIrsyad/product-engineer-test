package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

type heartbeatRequest struct {
	StoreCode string `json:"store_code"`
	DeviceID  string `json:"device_id"`
}

func (h *handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatRequest
	if err := decodeBody(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.StoreCode == "" || req.DeviceID == "" {
		writeError(w, r, fmt.Errorf("%w: store_code and device_id are required", domain.ErrInvalidInput))
		return
	}
	if err := h.svc.Heartbeat(r.Context(), req.StoreCode, req.DeviceID); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) syncPOS(w http.ResponseWriter, r *http.Request) {
	var in app.SyncPOSInput
	if err := decodeBody(r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	// Only the envelope is checked here. A malformed transaction is REJECTED on its own, so one
	// bad transaction never blocks the rest of the batch (ARCHITECTURE.md §13.2).
	if in.StoreCode == "" || in.DeviceID == "" || len(in.Transactions) == 0 {
		writeError(w, r, fmt.Errorf("%w: store_code, device_id and at least one transaction are required", domain.ErrInvalidInput))
		return
	}
	results, err := h.svc.SyncPOS(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// decodeBody reads a JSON body strictly: unknown fields are an error.
func decodeBody(r *http.Request, into any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w: body is not valid JSON for this request: %w", domain.ErrInvalidInput, err)
	}
	return nil
}
