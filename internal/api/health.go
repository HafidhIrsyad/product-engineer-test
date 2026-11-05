package api

import (
	"net/http"

	"github.com/rs/zerolog/hlog"
)

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Health(r.Context()); err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("health check failed")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
