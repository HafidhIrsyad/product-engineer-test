package app_test

import (
	"context"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

func (f fixture) orderStatus(t *testing.T, id int64) string {
	t.Helper()
	var s string
	require.NoError(t, f.pool.QueryRow(context.Background(), `SELECT status FROM orders WHERE id = $1`, id).Scan(&s))
	return s
}

func (f fixture) expireAt(t *testing.T, id int64, sql string) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `UPDATE orders SET expires_at = `+sql+` WHERE id = $1`, id)
	require.NoError(t, err)
}

func (f fixture) newOrder(t *testing.T, qty int32) app.OrderView {
	t.Helper()
	view, _, err := f.svc.CreateOrder(context.Background(), input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: qty}))
	require.NoError(t, err)
	return view
}

// T16: only unpaid orders past expires_at expire, their stock comes back exactly once,
// and running the sweeper again changes nothing.
func TestIntegration_ExpireDue(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 20})
	ctx := context.Background()

	var late, fresh []int64
	for i := range 7 {
		o := f.newOrder(t, 2)
		if i < 5 {
			f.expireAt(t, o.ID, `now() - interval '1 minute'`)
			late = append(late, o.ID)
		} else {
			fresh = append(fresh, o.ID)
		}
	}
	require.Equal(t, 14, testdb.Reserved(t, f.pool, f.skus["MGN-PROT-500"], f.loc))

	n, err := f.svc.ExpireDue(ctx)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	n, err = f.svc.ExpireDue(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "the second run finds nothing")

	for _, id := range late {
		assert.Equal(t, "EXPIRED", f.orderStatus(t, id))
	}
	for _, id := range fresh {
		assert.Equal(t, "PENDING_PAYMENT", f.orderStatus(t, id), "orders still inside their window are untouched")
	}
	assert.Equal(t, 4, testdb.Reserved(t, f.pool, f.skus["MGN-PROT-500"], f.loc), "5 orders × 2 units came back")
	testdb.AssertInvariants(t, f.pool)
}

func TestIntegration_ExpireDue_Concurrent(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 20})
	for range 5 {
		o := f.newOrder(t, 2)
		f.expireAt(t, o.ID, `now() - interval '1 minute'`)
	}

	var mu sync.Mutex
	total := 0
	runTogether(4, func(int) {
		n, err := f.svc.ExpireDue(context.Background())
		if assert.NoError(t, err) {
			mu.Lock()
			total += n
			mu.Unlock()
		}
	})

	assert.Equal(t, 5, total, "each order expires exactly once, however many sweepers run")
	assert.Equal(t, 0, testdb.Reserved(t, f.pool, f.skus["MGN-PROT-500"], f.loc))
	testdb.AssertInvariants(t, f.pool)
}

// T15: paying and expiring the same order at the same time. Either side may win, but an order is
// never PAID without its reservation, and PAID means the payment really succeeded.
func TestIntegration_PayVsExpiry(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 40})
	ctx := context.Background()

	for range 20 {
		o := f.newOrder(t, 1)
		f.expireAt(t, o.ID, `now() + interval '30 milliseconds'`)

		var payErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(rand.IntN(60)) * time.Millisecond)
			_, payErr = f.svc.PayOrder(ctx, o.ID)
		}()
		go func() {
			defer wg.Done()
			time.Sleep(time.Duration(25+rand.IntN(40)) * time.Millisecond)
			_, err := f.svc.ExpireDue(ctx)
			assert.NoError(t, err)
		}()
		wg.Wait()

		time.Sleep(40 * time.Millisecond) // now certainly past expires_at
		_, err := f.svc.ExpireDue(ctx)
		require.NoError(t, err)

		switch status := f.orderStatus(t, o.ID); status {
		case "PAID":
			assert.NoError(t, payErr, "PAID means the payment succeeded")
		case "EXPIRED":
			assert.ErrorIs(t, payErr, domain.ErrOrderExpired, "an expired order cannot have been paid")
		default:
			t.Fatalf("order ended as %s, want PAID or EXPIRED", status)
		}
	}
	testdb.AssertInvariants(t, f.pool) // reserved = open orders: PAID keeps its stock, EXPIRED gave it back
}
