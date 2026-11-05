package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	if err := decodeBody(w, r, &req); err != nil {
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
	if err := decodeBody(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	// Only the envelope is checked here. A malformed transaction is REJECTED on its own, so one
	// bad transaction never blocks the rest of the batch (ARCHITECTURE.md §13.2).
	if in.StoreCode == "" || in.DeviceID == "" || len(in.Transactions) == 0 {
		writeError(w, r, fmt.Errorf("%w: store_code, device_id and at least one transaction are required", domain.ErrInvalidInput))
		return
	}
	if len(in.Transactions) > maxPosTransactions || len(in.DeviceID) > maxDeviceIDLen {
		writeError(w, r, fmt.Errorf("%w: at most %d transactions per sync and a device_id of at most %d characters", domain.ErrInvalidInput, maxPosTransactions, maxDeviceIDLen))
		return
	}
	for _, t := range in.Transactions {
		if len(t.Items) > maxItemsPerTxn {
			writeError(w, r, fmt.Errorf("%w: at most %d items per transaction", domain.ErrInvalidInput, maxItemsPerTxn))
			return
		}
	}
	results, err := h.svc.SyncPOS(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// Input limits at the trust boundary: nobody can make the server read or loop over unbounded input.
const (
	maxBodyBytes       = 1 << 20 // 1 MiB
	maxPosTransactions = 500
	maxItemsPerTxn     = 100
	maxOrderItems      = 100
	maxDeviceIDLen     = 128
)

// decodeBody reads a JSON body strictly: size-limited, unknown fields and trailing data are errors.
func decodeBody(w http.ResponseWriter, r *http.Request, into any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%w: body is not valid JSON for this request: %w", domain.ErrInvalidInput, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: body must contain exactly one JSON object", domain.ErrInvalidInput)
	}
	return nil
}
