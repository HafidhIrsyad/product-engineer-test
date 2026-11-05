// Package testdb gives integration tests a real, migrated, empty PostgreSQL (ARCHITECTURE.md §18.3).
package testdb

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
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

// Store opens a store.Store on the same database as pool, without resetting it.
func Store(t *testing.T, p *pgxpool.Pool) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), p.Config().ConnString(), nil)
	require.NoError(t, err)
	t.Cleanup(st.Close)
	return st
}

// serialCounter makes every generated serial code unique across tests.
var serialCounter atomic.Int64

// InsertWarehouse creates a central warehouse that can sell online, with no buffer.
func InsertWarehouse(t *testing.T, p *pgxpool.Pool, code string) int64 {
	t.Helper()
	var id int64
	err := p.QueryRow(context.Background(), `
		INSERT INTO location (code, name, type, lat, lng, online_sellable)
		VALUES ($1, $1, 'CENTRAL_WAREHOUSE', -6.2088, 106.8456, true)
		RETURNING id`, code).Scan(&id)
	require.NoError(t, err)
	return id
}

// InsertSKU creates a SKU and returns its id.
func InsertSKU(t *testing.T, p *pgxpool.Pool, code string) int64 {
	t.Helper()
	var id int64
	err := p.QueryRow(context.Background(), `
		INSERT INTO sku (code, name, units_per_carton, cartons_per_pallet)
		VALUES ($1, $1, 12, 40) RETURNING id`, code).Scan(&id)
	require.NoError(t, err)
	return id
}

// InsertStore creates an own store that can sell online, with the given buffer.
func InsertStore(t *testing.T, p *pgxpool.Pool, code string, buffer int) int64 {
	t.Helper()
	var id int64
	err := p.QueryRow(context.Background(), `
		INSERT INTO location (code, name, type, lat, lng, online_sellable, online_buffer)
		VALUES ($1, $1, 'OWN_STORE', -6.9175, 107.6191, true, $2)
		RETURNING id`, code, buffer).Scan(&id)
	require.NoError(t, err)
	return id
}

// SetStock puts n physical units in a warehouse: n UNIT serials in IN_WAREHOUSE plus the
// matching on_hand counter, so invariant I1 holds. It returns the serial codes.
func SetStock(t *testing.T, p *pgxpool.Pool, skuID, locationID int64, n int) []string {
	t.Helper()
	return SetStockAs(t, p, skuID, locationID, n, "IN_WAREHOUSE")
}

// SetStockAs is SetStock with an explicit serial status (IN_STOCK for stores).
func SetStockAs(t *testing.T, p *pgxpool.Pool, skuID, locationID int64, n int, status string) []string {
	t.Helper()
	ctx := context.Background()

	var batchID int64
	require.NoError(t, p.QueryRow(ctx, `
		INSERT INTO production_batch (code, sku_id, produced_at, expires_on)
		VALUES ('B-' || $1::bigint || '-' || $2::bigint, $1::bigint, now(), current_date + 365)
		ON CONFLICT (code) DO UPDATE SET code = EXCLUDED.code
		RETURNING id`, skuID, locationID).Scan(&batchID))

	codes := make([]string, 0, n)
	for range n {
		code := fmt.Sprintf("MG%011d", serialCounter.Add(1))
		_, err := p.Exec(ctx, `
			INSERT INTO serial (code, level, sku_id, batch_id, status, location_id)
			VALUES ($1, 'UNIT', $2, $3, $4::serial_status, $5)`, code, skuID, batchID, status, locationID)
		require.NoError(t, err)
		codes = append(codes, code)
	}
	_, err := p.Exec(ctx, `
		INSERT INTO stock_balance (sku_id, location_id, on_hand) VALUES ($1, $2, $3)
		ON CONFLICT (sku_id, location_id) DO UPDATE SET on_hand = stock_balance.on_hand + EXCLUDED.on_hand`,
		skuID, locationID, n)
	require.NoError(t, err)
	return codes
}

// Reserved returns the reserved counter of a SKU at a location.
func Reserved(t *testing.T, p *pgxpool.Pool, skuID, locationID int64) int {
	t.Helper()
	var n int
	require.NoError(t, p.QueryRow(context.Background(),
		`SELECT reserved FROM stock_balance WHERE sku_id = $1 AND location_id = $2`, skuID, locationID).Scan(&n))
	return n
}
