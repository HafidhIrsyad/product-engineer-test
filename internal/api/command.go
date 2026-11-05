package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

func (h *handler) stock(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.StockOverview(r.Context(), r.URL.Query().Get("sku"), r.URL.Query().Get("location"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stock": rows})
}

func (h *handler) anomalies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, err := queryTime(q.Get("since"), time.Now().AddDate(0, 0, -30))
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := h.svc.Anomalies(r.Context(), app.AnomalyFilter{Type: q.Get("type"), Partner: q.Get("partner"), Since: since})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"anomalies": rows})
}

func (h *handler) leakage(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.Leakage(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"leakage": rows})
}

func (h *handler) sellOutSignal(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	to, err := queryTime(q.Get("to"), time.Now())
	if err != nil {
		writeError(w, r, err)
		return
	}
	from, err := queryTime(q.Get("from"), to.AddDate(0, 0, -28))
	if err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := h.svc.SellOutSignal(r.Context(), from, to)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signal": rows})
}

// queryTime reads an RFC 3339 time from a query parameter, or the fallback when it is empty.
func queryTime(raw string, fallback time.Time) (time.Time, error) {
	if raw == "" {
		return fallback, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: times are RFC 3339, like 2026-11-01T00:00:00Z", domain.ErrInvalidInput)
	}
	return t, nil
}
