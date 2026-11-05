// Package store is the PostgreSQL layer: sqlc-generated queries, transactions,
// migrations, and translation of driver errors into domain errors (ARCHITECTURE.md §4.2).
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns the connection pool.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects to PostgreSQL. A non-nil tracer logs every query (local only, §5.2).
func Open(ctx context.Context, databaseURL string, tracer pgx.QueryTracer) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	if tracer != nil {
		cfg.ConnConfig.Tracer = tracer
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Close releases every pooled connection.
func (s *Store) Close() { s.pool.Close() }
