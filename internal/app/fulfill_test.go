package app_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

func (f fixture) paidOrder(t *testing.T, qty int32) app.OrderView {
	t.Helper()
	o := f.newOrder(t, qty)
	_, err := f.svc.PayOrder(context.Background(), o.ID)
	require.NoError(t, err)
	return o
}

// T33: scanning the picked units turns the promise into sold units.
func TestIntegration_Fulfill(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 3})
	ctx := context.Background()
	codes := f.unitCodes(t)
	o := f.paidOrder(t, 2)

	view, err := f.svc.FulfillOrder(ctx, app.FulfillInput{OrderID: o.ID, SerialCodes: codes[:2]})
	require.NoError(t, err)
	assert.Equal(t, domain.OrderFulfilled, view.Status)
	assert.Equal(t, 2, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'SOLD' AND order_id = $1`, o.ID))
	assert.Equal(t, 1, f.count(t, `SELECT on_hand FROM stock_balance`))
	assert.Equal(t, 0, f.count(t, `SELECT reserved FROM stock_balance`))
	assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM stock_movement WHERE reason = 'SALE_ONLINE' AND delta_on_hand = -2 AND delta_reserved = -2`))
	testdb.AssertInvariants(t, f.pool)

	t.Run("the same units again change nothing", func(t *testing.T) {
		_, err := f.svc.FulfillOrder(ctx, app.FulfillInput{OrderID: o.ID, SerialCodes: []string{codes[1], codes[0]}})
		assert.NoError(t, err)
		_, err = f.svc.FulfillOrder(ctx, app.FulfillInput{OrderID: o.ID, SerialCodes: []string{codes[0], codes[2]}})
		assert.ErrorIs(t, err, domain.ErrInvalidOrderStatus, "other units for a fulfilled order")
		assert.Equal(t, 1, f.count(t, `SELECT on_hand FROM stock_balance`))
	})

	t.Run("the customer can verify a delivered unit", func(t *testing.T) {
		got, err := f.svc.Verify(ctx, app.VerifyInput{Code: domain.SerialCode(codes[0]), DeviceID: "phone-1"})
		require.NoError(t, err)
		assert.Equal(t, domain.VerdictGenuine, got.Verdict)
		assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'VERIFIED' AND code = $1`, codes[0]))
	})
}

// T34: every rule that refuses a fulfillment leaves the order and the stock untouched.
func TestIntegration_Fulfill_Refused(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 4})
	ctx := context.Background()
	codes := f.unitCodes(t)
	otherSKU := testdb.SetStock(t, f.pool, testdb.InsertSKU(t, f.pool, "MGN-COLL-200"), f.loc, 1)
	elsewhere := testdb.InsertWarehouse(t, f.pool, "WH-SBY")
	farUnit := testdb.SetStock(t, f.pool, f.skus["MGN-PROT-500"], elsewhere, 1)
	pending := f.newOrder(t, 1)
	paid := f.paidOrder(t, 2)

	refuse := func(name string, id int64, serials []string, want error) {
		t.Helper()
		_, err := f.svc.FulfillOrder(ctx, app.FulfillInput{OrderID: id, SerialCodes: serials})
		assert.ErrorIs(t, err, want, name)
	}
	refuse("not paid yet", pending.ID, codes[:1], domain.ErrInvalidOrderStatus)
	refuse("unknown order", 9999, codes[:1], domain.ErrOrderNotFound)
	refuse("no codes", paid.ID, nil, domain.ErrInvalidInput)
	refuse("too few units", paid.ID, codes[:1], domain.ErrInvalidInput)
	refuse("too many units", paid.ID, codes[:3], domain.ErrInvalidInput)
	refuse("a code listed twice", paid.ID, []string{codes[0], codes[0]}, domain.ErrInvalidInput)
	refuse("a unit of another product", paid.ID, []string{codes[0], otherSKU[0]}, domain.ErrInvalidInput)
	refuse("a unit at another location", paid.ID, []string{codes[0], farUnit[0]}, domain.ErrInvalidInput)
	refuse("an unknown code", paid.ID, []string{codes[0], string(domain.NewSerialCode())}, domain.ErrSerialNotFound)

	_, err := f.pool.Exec(ctx, `UPDATE serial SET status = 'QUARANTINE' WHERE code = $1`, codes[1])
	require.NoError(t, err)
	refuse("a unit that is not for sale", paid.ID, codes[:2], domain.ErrInvalidTransition)

	assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'SOLD'`))
	assert.Equal(t, "PAID", f.orderStatus(t, paid.ID))
	assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM stock_movement WHERE reason = 'SALE_ONLINE'`), "no stock left")
}

// T35: fulfill and cancel of the same order at the same time: exactly one wins.
func TestIntegration_Fulfill_VsCancel(t *testing.T) {
	const orders = 20
	f := newFixture(t, map[string]int{"MGN-PROT-500": orders})
	ctx := context.Background()
	codes := f.unitCodes(t)
	ids := make([]int64, orders)
	for i := range ids {
		ids[i] = f.paidOrder(t, 1).ID
	}

	fulfilled, cancelled := 0, 0
	var mu sync.Mutex
	runTogether(2*orders, func(i int) {
		n := i / 2
		if i%2 == 0 {
			_, err := f.svc.FulfillOrder(ctx, app.FulfillInput{OrderID: ids[n], SerialCodes: codes[n : n+1]})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				fulfilled++
			} else {
				assert.ErrorIs(t, err, domain.ErrInvalidOrderStatus)
			}
			return
		}
		_, err := f.svc.CancelOrder(ctx, ids[n])
		mu.Lock()
		defer mu.Unlock()
		if err == nil {
			cancelled++
		} else {
			assert.ErrorIs(t, err, domain.ErrOrderNotCancellable)
		}
	})

	assert.Equal(t, orders, fulfilled+cancelled, "every order ended up fulfilled or cancelled, never both")
	assert.Equal(t, fulfilled, f.count(t, `SELECT COUNT(*) FROM orders WHERE status = 'FULFILLED'`))
	assert.Equal(t, fulfilled, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'SOLD'`))
	testdb.AssertInvariants(t, f.pool)
}

// T36: the last unit is picked for an online order while the cashier sells it: it is sold once.
func TestIntegration_Fulfill_VsPosSale(t *testing.T) {
	for range 10 {
		f := newStore(t, 1, 0)
		ctx := context.Background()
		o, _, err := f.svc.CreateOrder(ctx, app.CreateOrderInput{
			IdempotencyKey: uuid.New(), Channel: domain.ChannelD2C, LocationCode: storeCode,
			Items: []app.OrderItem{{SKUCode: skuCode, Qty: 1}},
		})
		require.NoError(t, err)
		_, err = f.svc.PayOrder(ctx, o.ID)
		require.NoError(t, err)

		runTogether(2, func(i int) {
			if i == 0 {
				_, err := f.svc.FulfillOrder(ctx, app.FulfillInput{OrderID: o.ID, SerialCodes: f.serials})
				if err != nil {
					assert.True(t, errorIsOneOf(err, domain.ErrInvalidTransition, domain.ErrInvalidOrderStatus), "unexpected error: %v", err)
				}
				return
			}
			_, err := f.svc.SyncPOS(ctx, syncOf(sale(f.serials[0])))
			assert.NoError(t, err)
		})

		assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'SOLD'`))
		assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM stock_movement WHERE reason IN ('SALE_POS', 'SALE_ONLINE')`), "stock left once")
		assert.Equal(t, 0, f.onHand(t))
		testdb.AssertInvariants(t, f.pool)
	}
}
