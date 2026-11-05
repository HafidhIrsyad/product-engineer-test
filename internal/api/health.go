package api

import (
	"errors"
	"net/http"

	"github.com/rs/zerolog/hlog"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	err := h.svc.Health(r.Context())
	if errors.Is(err, domain.ErrShuttingDown) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "shutting_down"})
		return
	}
	if err != nil {
		hlog.FromRequest(r).Error().Err(err).Msg("health check failed")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
