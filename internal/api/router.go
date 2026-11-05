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
		r.Post("/production/batches", h.createBatch)
		r.Get("/serials/{code}", h.traceSerial)
		r.Post("/serials/{code}/quarantine", h.quarantine)
		r.Post("/serials/{code}/release", h.releaseQuarantine)
		r.Get("/stock", h.stock)
		r.Get("/anomalies", h.anomalies)
		r.Get("/reports/leakage", h.leakage)
		r.Get("/reports/sell-out-signal", h.sellOutSignal)
		r.Post("/points/claims", h.claimPoints)
		r.Get("/points/members/{id}", h.memberPoints)
		r.Post("/shipments/ship", h.ship)
		r.Post("/shipments/receive", h.receive)
		r.Post("/orders", h.createOrder)
		r.Get("/orders/{id}", h.getOrder)
		r.Post("/orders/{id}/pay", h.payOrder)
		r.Post("/orders/{id}/cancel", h.cancelOrder)
		r.Post("/orders/{id}/fulfill", h.fulfillOrder)
		r.Post("/orders/{id}/reallocate", h.reallocateOrder)
		r.Post("/pos/sync", h.syncPOS)
		r.Post("/pos/heartbeat", h.heartbeat)
		r.Get("/pos/gaps", h.posGaps)
	})
	return r
}
