// Package testdb gives integration tests a real, migrated, empty PostgreSQL (ARCHITECTURE.md §18.3).
package testdb

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// image must match docker-compose.yml.
const image = "postgres:18-alpine"

var (
	once    sync.Once
	pool    *pgxpool.Pool
	initErr error
)

// Pool returns a connection to a real PostgreSQL that has been migrated and emptied.
// The container starts once per test package and is removed by testcontainers when the
// test process exits. In -short mode the test is skipped; without Docker it fails.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skipped in -short mode")
	}
	once.Do(func() { pool, initErr = start(context.Background()) })
	require.NoError(t, initErr, "start PostgreSQL test container (is Docker running?)")
	reset(t, pool)
	return pool
}

func start(ctx context.Context) (*pgxpool.Pool, error) {
	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, err
	}
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, err
	}
	// Same migrations as the API, so tests run against the production schema.
	if err := store.Migrate(ctx, dsn); err != nil {
		return nil, err
	}
	return pgxpool.New(ctx, dsn)
}

// reset empties every application table. goose_db_version is kept, so migrations do not rerun.
func reset(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	_, err := p.Exec(context.Background(), `
		TRUNCATE sku, partner, location, pos_device, production_batch, orders, order_item,
		         serial, serial_event, stock_balance, stock_movement,
		         pos_transaction, pos_transaction_item, scan_event, anomaly
		RESTART IDENTITY CASCADE`)
	require.NoError(t, err, "reset database")
}

// AssertInvariants checks the stock invariants of ARCHITECTURE.md §11.2:
//
//	I1: on_hand  = number of UNIT serials at the location in IN_WAREHOUSE, IN_STOCK or QUARANTINE
//	I2: reserved = sum of order_item.qty of PENDING_PAYMENT / PAID orders at the location
func AssertInvariants(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	var i1 int
	err := p.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM stock_balance b
		FULL OUTER JOIN (
			SELECT sku_id, location_id, COUNT(*) AS cnt
			FROM serial
			WHERE level = 'UNIT' AND status IN ('IN_WAREHOUSE', 'IN_STOCK', 'QUARANTINE')
			GROUP BY sku_id, location_id
		) s ON s.sku_id = b.sku_id AND s.location_id = b.location_id
		WHERE COALESCE(b.on_hand, 0) <> COALESCE(s.cnt, 0)`).Scan(&i1)
	require.NoError(t, err)
	require.Zero(t, i1, "I1 violated: on_hand differs from serial count for %d (sku, location) pairs", i1)

	var i2 int
	err = p.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM stock_balance b
		FULL OUTER JOIN (
			SELECT oi.sku_id, o.location_id, SUM(oi.qty) AS qty
			FROM orders o
			JOIN order_item oi ON oi.order_id = o.id
			WHERE o.status IN ('PENDING_PAYMENT', 'PAID')
			GROUP BY oi.sku_id, o.location_id
		) r ON r.sku_id = b.sku_id AND r.location_id = b.location_id
		WHERE COALESCE(b.reserved, 0) <> COALESCE(r.qty, 0)`).Scan(&i2)
	require.NoError(t, err)
	require.Zero(t, i2, "I2 violated: reserved differs from open order quantities for %d (sku, location) pairs", i2)
}
