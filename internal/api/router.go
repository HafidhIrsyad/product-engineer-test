package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/config"
	"github.com/HafidhIrsyad/product-engineer-test/internal/logging"
)

type handler struct {
	svc *app.Service
}

// NewRouter registers every route in one place.
func NewRouter(svc *app.Service, logger zerolog.Logger, cfg config.HTTP) http.Handler {
	h := &handler{svc: svc}

	r := chi.NewRouter()
	r.Use(logging.Middleware(logger)...)
	r.Use(middleware.Recoverer, middleware.Timeout(cfg.RequestTimeout))

	r.Get("/healthz", h.health)
	r.Route("/v1", func(r chi.Router) {
		r.Post("/verify", h.verify)
		r.Post("/orders", h.createOrder)
		r.Get("/orders/{id}", h.getOrder)
		r.Post("/orders/{id}/pay", h.payOrder)
		r.Post("/orders/{id}/cancel", h.cancelOrder)
		r.Post("/pos/sync", h.syncPOS)
		r.Post("/pos/heartbeat", h.heartbeat)
	})
	return r
}
