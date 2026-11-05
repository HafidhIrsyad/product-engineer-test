package api

import (
	"net/http"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
)

func (h *handler) ship(w http.ResponseWriter, r *http.Request) {
	var in app.ShipInput
	if err := decodeBody(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.svc.Ship(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *handler) receive(w http.ResponseWriter, r *http.Request) {
	var in app.ReceiveInput
	if err := decodeBody(w, r, &in); err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.svc.Receive(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}
