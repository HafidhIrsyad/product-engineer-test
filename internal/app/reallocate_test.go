package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

// lostOrder creates an online order at the store and lets the cashier sell the unit it was promised.
func lostOrder(t *testing.T, f posFixture, paid bool) app.OrderView {
	t.Helper()
	ctx := context.Background()
	o, _, err := f.svc.CreateOrder(ctx, app.CreateOrderInput{
		IdempotencyKey: uuid.New(), Channel: domain.ChannelD2C, LocationCode: storeCode,
		Items: []app.OrderItem{{SKUCode: skuCode, Qty: 1}},
	})
	require.NoError(t, err)
	if paid {
		_, err = f.svc.PayOrder(ctx, o.ID)
		require.NoError(t, err)
	}
	_, err = f.svc.SyncPOS(ctx, syncOf(sale(f.serials[0])))
	require.NoError(t, err)
	require.Equal(t, "NEEDS_REALLOCATION", f.status(t, o.ID))
	return o
}

// T37: a lost order moves to another location and comes back as it was.
func TestIntegration_Reallocate(t *testing.T) {
	for _, paid := range []bool{true, false} {
		f := newStore(t, 1, 0)
		ctx := context.Background()
		warehouse := testdb.InsertWarehouse(t, f.pool, "WH-JKT")
		testdb.SetStock(t, f.pool, f.skuID, warehouse, 1)
		o := lostOrder(t, f, paid)

		view, err := f.svc.ReallocateOrder(ctx, o.ID, "WH-JKT")
		require.NoError(t, err)
		assert.Equal(t, "WH-JKT", view.LocationCode)
		want := domain.OrderPendingPayment
		if paid {
			want = domain.OrderPaid
		}
		assert.Equal(t, want, view.Status, "paid=%v: the order returns to the status it had", paid)
		assert.Equal(t, 1, testdb.Reserved(t, f.pool, f.skuID, warehouse))
		assert.Equal(t, 0, testdb.Reserved(t, f.pool, f.skuID, f.storeID))
		testdb.AssertInvariants(t, f.pool)

		_, err = f.svc.ReallocateOrder(ctx, o.ID, "WH-JKT")
		assert.ErrorIs(t, err, domain.ErrInvalidOrderStatus, "no longer waiting for reallocation")
	}
}

// T38: with nowhere to go the order stays as it was.
func TestIntegration_Reallocate_Refused(t *testing.T) {
	f := newStore(t, 1, 0)
	ctx := context.Background()
	testdb.InsertWarehouse(t, f.pool, "WH-JKT") // no stock there
	o := lostOrder(t, f, true)

	_, err := f.svc.ReallocateOrder(ctx, o.ID, "WH-JKT")
	assert.ErrorIs(t, err, domain.ErrInsufficientStock)
	assert.Equal(t, "NEEDS_REALLOCATION", f.status(t, o.ID))
	_, err = f.svc.ReallocateOrder(ctx, o.ID, "NOPE")
	assert.ErrorIs(t, err, domain.ErrUnknownLocation)
	_, err = f.svc.ReallocateOrder(ctx, 9999, "WH-JKT")
	assert.ErrorIs(t, err, domain.ErrOrderNotFound)
	testdb.AssertInvariants(t, f.pool)
}

// T42: a sale whose stock record is broken is rejected, and the sales around it still go through.
func TestIntegration_PosSync_OneBrokenSaleDoesNotBlockTheBatch(t *testing.T) {
	f := newStore(t, 5, 0)
	ctx := context.Background()
	other := testdb.InsertSKU(t, f.pool, "MGN-COLL-200")
	_, brokenCode := testdb.InsertSerial(t, f.pool, testdb.SerialSpec{SKUID: other, LocationID: f.storeID, Status: "IN_STOCK"}) // no stock_balance row for it
	first := sale(f.serials[0])                                                                                                 // sequence numbers grow with each call, so the broken sale sits in the middle
	broken := sale(brokenCode)
	broken.Items[0].SKUCode = "MGN-COLL-200"
	last := sale(f.serials[1])

	results, err := f.svc.SyncPOS(ctx, syncOf(first, broken, last))
	require.NoError(t, err)
	require.Len(t, results, 3)
	assert.Equal(t, []domain.PosStatus{domain.PosAccepted, domain.PosRejected, domain.PosAccepted}, statuses(results))
	assert.Equal(t, string(domain.RejectDataInconsistent), results[1].Error)
	assert.Equal(t, 3, f.onHand(t), "the two good sales took their stock")
	assert.Equal(t, 2, f.count(t, `SELECT COUNT(*) FROM pos_transaction`), "the rejected sale left no trace but the log")
}

// T43: a skipped sequence number shows where a sale may have been lost.
func TestIntegration_PosGaps(t *testing.T) {
	f := newStore(t, 6, 0)
	ctx := context.Background()
	var txns []domain.PosTxn
	for i, seq := range []int64{1, 2, 5, 6} {
		txn := sale(f.serials[i])
		txn.LocalSeq = seq
		txns = append(txns, txn)
	}
	_, err := f.svc.SyncPOS(ctx, syncOf(txns...))
	require.NoError(t, err)

	gaps, err := f.svc.PosGaps(ctx, storeCode)
	require.NoError(t, err)
	assert.Equal(t, []app.PosGap{{DeviceID: "pos-bdg-01-a", FromSeq: 3, ToSeq: 4}}, gaps)

	_, err = f.svc.PosGaps(ctx, "NOPE")
	assert.ErrorIs(t, err, domain.ErrStoreNotFound)
}
