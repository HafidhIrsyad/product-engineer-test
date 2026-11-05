package app_test

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

type fixture struct {
	pool *pgxpool.Pool
	svc  *app.Service
	loc  int64
	skus map[string]int64
}

// newFixture creates one warehouse "WH-JKT" and the given SKUs, each with the given stock.
func newFixture(t *testing.T, stock map[string]int) fixture {
	t.Helper()
	pool := testdb.Pool(t)
	f := fixture{pool: pool, svc: app.NewService(testdb.Store(t, pool)), skus: map[string]int64{}}
	f.loc = testdb.InsertWarehouse(t, pool, "WH-JKT")
	for code, n := range stock {
		f.skus[code] = testdb.InsertSKU(t, pool, code)
		testdb.SetStock(t, pool, f.skus[code], f.loc, n)
	}
	return f
}

func input(key uuid.UUID, items ...app.OrderItem) app.CreateOrderInput {
	return app.CreateOrderInput{IdempotencyKey: key, Channel: domain.ChannelD2C, LocationCode: "WH-JKT", Items: items}
}

// runTogether starts n goroutines that all begin at the same moment.
func runTogether(n int, fn func(i int)) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			fn(i)
		}()
	}
	close(start)
	wg.Wait()
}

// T1: 100 buyers race for 10 units. Exactly 10 win; nobody gets an unexpected error.
func TestIntegration_CreateOrder_NoOversell(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 10})

	var ok atomic.Int32
	runTogether(100, func(int) {
		_, created, err := f.svc.CreateOrder(context.Background(), input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}))
		if err == nil {
			assert.True(t, created)
			ok.Add(1)
			return
		}
		// assert, not require: require calls t.FailNow, which must not run in another goroutine.
		assert.ErrorIs(t, err, domain.ErrInsufficientStock)
	})

	require.EqualValues(t, 10, ok.Load(), "orders that succeeded")
	assert.Equal(t, 10, testdb.Reserved(t, f.pool, f.skus["MGN-PROT-500"], f.loc))
	testdb.AssertInvariants(t, f.pool)
}

// T2: orders listing the same two SKUs in opposite order never deadlock, because lines are
// sorted by sku_id before locking. Any 40P01 would surface here as an unexpected error.
func TestIntegration_CreateOrder_NoDeadlock(t *testing.T) {
	f := newFixture(t, map[string]int{"SKU-A": 30, "SKU-B": 30})
	a, b := app.OrderItem{SKUCode: "SKU-A", Qty: 1}, app.OrderItem{SKUCode: "SKU-B", Qty: 1}

	var ok atomic.Int32
	runTogether(100, func(i int) {
		items := []app.OrderItem{a, b}
		if i%2 == 1 {
			items = []app.OrderItem{b, a}
		}
		_, _, err := f.svc.CreateOrder(context.Background(), input(uuid.New(), items...))
		if err == nil {
			ok.Add(1)
			return
		}
		assert.ErrorIs(t, err, domain.ErrInsufficientStock)
	})

	require.EqualValues(t, 30, ok.Load(), "orders that succeeded")
	assert.Equal(t, 30, testdb.Reserved(t, f.pool, f.skus["SKU-A"], f.loc))
	assert.Equal(t, 30, testdb.Reserved(t, f.pool, f.skus["SKU-B"], f.loc))
	testdb.AssertInvariants(t, f.pool)
}

// T8: the same Idempotency-Key never creates a second order or reserves twice.
func TestIntegration_CreateOrder_Idempotent(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 10})
	item := app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 2}

	t.Run("one after another", func(t *testing.T) {
		key := uuid.New()
		first, created, err := f.svc.CreateOrder(context.Background(), input(key, item))
		require.NoError(t, err)
		require.True(t, created)

		second, created, err := f.svc.CreateOrder(context.Background(), input(key, item))
		require.NoError(t, err)
		assert.False(t, created, "second call returns the existing order")
		assert.Equal(t, first.ID, second.ID)
		assert.Equal(t, 2, testdb.Reserved(t, f.pool, f.skus["MGN-PROT-500"], f.loc))
	})

	t.Run("at the same time", func(t *testing.T) {
		key := uuid.New()
		ids := make([]int64, 10)
		var createdCount atomic.Int32
		runTogether(10, func(i int) {
			v, created, err := f.svc.CreateOrder(context.Background(), input(key, item))
			if !assert.NoError(t, err) {
				return
			}
			ids[i] = v.ID
			if created {
				createdCount.Add(1)
			}
		})

		assert.EqualValues(t, 1, createdCount.Load(), "exactly one request creates the order")
		for _, id := range ids {
			assert.Equal(t, ids[0], id, "every request sees the same order")
		}
		assert.Equal(t, 4, testdb.Reserved(t, f.pool, f.skus["MGN-PROT-500"], f.loc), "2 from the first subtest + 2 now")
		testdb.AssertInvariants(t, f.pool)
	})
}

// T9: cancelling the same order 10 times at once releases its stock exactly once.
func TestIntegration_CancelOrder_Idempotent(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 10})
	view, _, err := f.svc.CreateOrder(context.Background(), input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 3}))
	require.NoError(t, err)
	require.Equal(t, 3, testdb.Reserved(t, f.pool, f.skus["MGN-PROT-500"], f.loc))

	runTogether(10, func(int) {
		v, err := f.svc.CancelOrder(context.Background(), view.ID)
		if assert.NoError(t, err) {
			assert.Equal(t, domain.OrderCancelled, v.Status)
		}
	})

	assert.Equal(t, 0, testdb.Reserved(t, f.pool, f.skus["MGN-PROT-500"], f.loc))
	var releases int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM stock_movement WHERE reason = 'RELEASE' AND ref_id = $1`, strconv.FormatInt(view.ID, 10)).Scan(&releases))
	assert.Equal(t, 1, releases, "stock released exactly once")
	testdb.AssertInvariants(t, f.pool)
}

func TestIntegration_CancelOrder_Rules(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 10})
	ctx := context.Background()

	_, err := f.svc.CancelOrder(ctx, 999999)
	assert.ErrorIs(t, err, domain.ErrOrderNotFound)

	view, _, err := f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}))
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE orders SET status = 'FULFILLED' WHERE id = $1`, view.ID)
	require.NoError(t, err)
	_, err = f.svc.CancelOrder(ctx, view.ID)
	assert.ErrorIs(t, err, domain.ErrOrderNotCancellable)
}

func TestIntegration_CreateOrder_UnknownReferences(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 10})
	ctx := context.Background()

	_, _, err := f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "NOPE", Qty: 1}))
	assert.ErrorIs(t, err, domain.ErrUnknownSKU)

	in := input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1})
	in.LocationCode = "NOPE"
	_, _, err = f.svc.CreateOrder(ctx, in)
	assert.ErrorIs(t, err, domain.ErrUnknownLocation)
}

func TestIntegration_PayOrder(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 10})
	ctx := context.Background()
	view, _, err := f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}))
	require.NoError(t, err)

	paid, err := f.svc.PayOrder(ctx, view.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderPaid, paid.Status)

	again, err := f.svc.PayOrder(ctx, view.ID)
	require.NoError(t, err, "paying twice is idempotent")
	assert.Equal(t, domain.OrderPaid, again.Status)

	_, err = f.svc.PayOrder(ctx, 999999)
	assert.ErrorIs(t, err, domain.ErrOrderNotFound)

	// An order past expires_at cannot be paid, even before the sweeper marks it EXPIRED.
	late, _, err := f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}))
	require.NoError(t, err)
	_, err = f.pool.Exec(ctx, `UPDATE orders SET expires_at = now() - interval '1 minute' WHERE id = $1`, late.ID)
	require.NoError(t, err)
	_, err = f.svc.PayOrder(ctx, late.ID)
	assert.ErrorIs(t, err, domain.ErrOrderExpired)

	cancelled, _, err := f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}))
	require.NoError(t, err)
	_, err = f.svc.CancelOrder(ctx, cancelled.ID)
	require.NoError(t, err)
	_, err = f.svc.PayOrder(ctx, cancelled.ID)
	assert.ErrorIs(t, err, domain.ErrInvalidOrderStatus)
}
