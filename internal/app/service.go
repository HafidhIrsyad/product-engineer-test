package app

import (
	"context"

	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// Service is the single entry point for every use case.
type Service struct {
	store *store.Store
}

func NewService(s *store.Store) *Service {
	return &Service{store: s}
}

// Health reports whether the database is reachable.
func (s *Service) Health(ctx context.Context) error {
	return s.store.Ping(ctx)
}
