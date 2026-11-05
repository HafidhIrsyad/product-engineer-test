// Package api is the HTTP layer: chi router, handlers, input decoding (the trust boundary),
// and the only place where errors become HTTP status codes (ARCHITECTURE.md §15–16).
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
	// Order matters (§5.2): request logs outside, recoverer inside,
	// so a panic is still logged as "request completed" with status 500.
	r.Use(logging.Middleware(logger)...)
	r.Use(middleware.Recoverer, middleware.Timeout(cfg.RequestTimeout))

	r.Get("/healthz", h.health)
	r.Route("/v1", func(r chi.Router) {
		r.Post("/orders", h.createOrder)
		r.Post("/orders/{id}/pay", h.payOrder)
		r.Post("/orders/{id}/cancel", h.cancelOrder)
	})
	return r
}
