package app

import (
	"context"
	"sync/atomic"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// Service is the single entry point for every use case.
type Service struct {
	store    *store.Store
	draining atomic.Bool
}

func NewService(s *store.Store) *Service {
	return &Service{store: s}
}

// Drain marks the service as shutting down: Health fails from now on, so a load balancer stops
// sending new traffic while requests already running finish.
func (s *Service) Drain() { s.draining.Store(true) }

// Health reports whether the service can take traffic: not draining, and the database reachable.
func (s *Service) Health(ctx context.Context) error {
	if s.draining.Load() {
		return domain.ErrShuttingDown
	}
	return s.store.Ping(ctx)
}
