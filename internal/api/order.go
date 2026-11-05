package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

func (h *handler) createOrder(w http.ResponseWriter, r *http.Request) {
	in, err := decodeCreateOrder(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, created, err := h.svc.CreateOrder(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK // same Idempotency-Key: the existing order
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, view)
}

func (h *handler) getOrder(w http.ResponseWriter, r *http.Request) {
	id, err := orderID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.svc.GetOrder(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) payOrder(w http.ResponseWriter, r *http.Request) {
	id, err := orderID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.svc.PayOrder(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) cancelOrder(w http.ResponseWriter, r *http.Request) {
	id, err := orderID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.svc.CancelOrder(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// decodeCreateOrder is the trust boundary: raw request → validated app input (ARCHITECTURE.md §16.1).
func decodeCreateOrder(r *http.Request) (app.CreateOrderInput, error) {
	var in app.CreateOrderInput

	key, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil {
		return in, fmt.Errorf("%w: header Idempotency-Key must be a UUID", domain.ErrInvalidInput)
	}
	in.IdempotencyKey = key

	if err := decodeBody(r, &in); err != nil {
		return in, err
	}

	if in.Channel != domain.ChannelD2C && in.Channel != domain.ChannelMarketplace {
		return in, fmt.Errorf("%w: channel must be D2C or MARKETPLACE", domain.ErrInvalidInput)
	}
	if in.LocationCode == "" {
		return in, fmt.Errorf("%w: location_code is required", domain.ErrInvalidInput)
	}
	if len(in.Items) == 0 {
		return in, fmt.Errorf("%w: items must not be empty", domain.ErrInvalidInput)
	}
	seen := make(map[string]bool, len(in.Items))
	for _, it := range in.Items {
		switch {
		case it.SKUCode == "":
			return in, fmt.Errorf("%w: sku_code is required", domain.ErrInvalidInput)
		case it.Qty <= 0:
			return in, fmt.Errorf("%w: qty of %s must be greater than 0", domain.ErrInvalidInput, it.SKUCode)
		case seen[it.SKUCode]:
			return in, fmt.Errorf("%w: sku_code %s appears twice", domain.ErrInvalidInput, it.SKUCode)
		}
		seen[it.SKUCode] = true
	}
	return in, nil
}

func orderID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: order id must be a positive integer", domain.ErrInvalidInput)
	}
	return id, nil
}
