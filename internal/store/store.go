// Package store is the PostgreSQL layer: sqlc-generated queries, transactions,
// migrations, and driver error helpers (ARCHITECTURE.md §4.2).
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns the connection pool. The embedded Queries run directly on the pool;
// inside InTx they run on one transaction.
type Store struct {
	*Queries
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
	return &Store{Queries: New(pool), pool: pool}, nil
}

// InTx runs fn in one transaction: commit when fn returns nil, rollback otherwise.
// One business operation = one transaction (§4.2).
func (s *Store) InTx(ctx context.Context, fn func(q *Queries) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(s.WithTx(tx))
	})
}

// Ping checks that the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Close releases every pooled connection.
func (s *Store) Close() { s.pool.Close() }

// IsNotFound reports whether a :one query matched no row.
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
