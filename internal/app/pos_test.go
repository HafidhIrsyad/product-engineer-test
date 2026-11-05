package app_test

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

const (
	storeCode = "STORE-BDG-01"
	skuCode   = "MGN-PROT-500"
)

type posFixture struct {
	pool    *pgxpool.Pool
	svc     *app.Service
	storeID int64
	skuID   int64
	serials []string // units in stock at the store
}

// newStore creates the demo store with n units in stock and the given online buffer.
func newStore(t *testing.T, n, buffer int) posFixture {
	t.Helper()
	pool := testdb.Pool(t)
	f := posFixture{pool: pool, svc: app.NewService(testdb.Store(t, pool))}
	f.storeID = testdb.InsertStore(t, pool, storeCode, buffer)
	f.skuID = testdb.InsertSKU(t, pool, skuCode)
	f.serials = testdb.SetStockAs(t, pool, f.skuID, f.storeID, n, "IN_STOCK")
	return f
}

var seqCounter atomic.Int64

// sale is one cashier transaction selling the given serials, with a fresh id and local_seq.
func sale(serials ...string) domain.PosTxn {
	items := make([]domain.PosItem, len(serials))
	for i, s := range serials {
		items[i] = domain.PosItem{SerialCode: s, SKUCode: skuCode, Price: 450000}
	}
	return domain.PosTxn{
		ID: uuid.New(), LocalSeq: seqCounter.Add(1), OccurredAt: time.Now(),
		PaymentMethod: "CASH", TotalAmount: int64(450000 * len(serials)), Items: items,
	}
}

func syncOf(txns ...domain.PosTxn) app.SyncPOSInput {
	return app.SyncPOSInput{StoreCode: storeCode, DeviceID: "pos-bdg-01-a", Transactions: txns}
}

func (f posFixture) onHand(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(),
		`SELECT on_hand FROM stock_balance WHERE sku_id = $1 AND location_id = $2`, f.skuID, f.storeID).Scan(&n))
	return n
}

func (f posFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

func statuses(results []app.PosResult) []domain.PosStatus {
	out := make([]domain.PosStatus, len(results))
	for i, r := range results {
		out[i] = r.Status
	}
	return out
}

// T5: sending the same batch again changes nothing.
func TestIntegration_PosSync_Duplicate(t *testing.T) {
	f := newStore(t, 5, 0)
	ctx := context.Background()
	batch := syncOf(sale(f.serials[0]), sale(f.serials[1], f.serials[2]))

	first, err := f.svc.SyncPOS(ctx, batch)
	require.NoError(t, err)
	assert.Equal(t, []domain.PosStatus{domain.PosAccepted, domain.PosAccepted}, statuses(first))
	require.Equal(t, 2, f.onHand(t))

	second, err := f.svc.SyncPOS(ctx, batch)
	require.NoError(t, err)
	assert.Equal(t, []domain.PosStatus{domain.PosDuplicate, domain.PosDuplicate}, statuses(second))
	assert.Equal(t, "ACCEPTED", second[0].OriginalResult)

	assert.Equal(t, 2, f.onHand(t), "stock was taken once")
	assert.Equal(t, 2, f.count(t, `SELECT COUNT(*) FROM pos_transaction`))
	testdb.AssertInvariants(t, f.pool)
}

// T6: the same batch sent 10 times at once is processed exactly once.
func TestIntegration_PosSync_Concurrent(t *testing.T) {
	f := newStore(t, 5, 0)
	batch := syncOf(sale(f.serials[0]), sale(f.serials[1]))

	var mu sync.Mutex
	perID := map[uuid.UUID][]domain.PosStatus{}
	runTogether(10, func(int) {
		results, err := f.svc.SyncPOS(context.Background(), batch)
		if !assert.NoError(t, err) {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, r := range results {
			perID[r.ID] = append(perID[r.ID], r.Status)
		}
	})

	require.Len(t, perID, 2)
	for id, got := range perID {
		assert.Len(t, got, 10, id)
		assert.Equal(t, 1, countOf(got, domain.PosAccepted), "exactly one ACCEPTED for %s", id)
		assert.Equal(t, 9, countOf(got, domain.PosDuplicate), "nine DUPLICATE for %s", id)
	}
	assert.Equal(t, 3, f.onHand(t), "two units were taken once")
	testdb.AssertInvariants(t, f.pool)
}

func countOf(all []domain.PosStatus, want domain.PosStatus) int {
	n := 0
	for _, s := range all {
		if s == want {
			n++
		}
	}
	return n
}

// T7: the last unit is sold at the cashier while an online order holds it.
// The physical sale wins; the online order loses its reservation.
func TestIntegration_PosSync_SplitBrain(t *testing.T) {
	f := newStore(t, 1, 0)
	ctx := context.Background()
	require.NoError(t, f.svc.Heartbeat(ctx, storeCode, "pos-bdg-01-a"))

	order, _, err := f.svc.CreateOrder(ctx, app.CreateOrderInput{
		IdempotencyKey: uuid.New(), Channel: domain.ChannelD2C, LocationCode: storeCode,
		Items: []app.OrderItem{{SKUCode: skuCode, Qty: 1}},
	})
	require.NoError(t, err, "the online order is accepted while the store looks available")

	results, err := f.svc.SyncPOS(ctx, syncOf(sale(f.serials[0])))
	require.NoError(t, err)
	assert.Equal(t, domain.PosAccepted, results[0].Status, "the cashier sale is always accepted")

	var status string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, order.ID).Scan(&status))
	assert.Equal(t, string(domain.OrderNeedsReallocation), status)
	assert.Equal(t, 0, testdb.Reserved(t, f.pool, f.skuID, f.storeID))
	assert.Equal(t, 0, f.onHand(t))
	assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM anomaly WHERE type = 'OVERSOLD' AND ref_id = $1`, strconv.FormatInt(order.ID, 10)))
	testdb.AssertInvariants(t, f.pool)
}

// Only the newest order is sacrificed, and only as many as the shortfall needs.
func TestIntegration_PosSync_SacrificesNewestOrderFirst(t *testing.T) {
	f := newStore(t, 3, 0)
	ctx := context.Background()
	var ids []int64
	for range 3 {
		o, _, err := f.svc.CreateOrder(ctx, app.CreateOrderInput{
			IdempotencyKey: uuid.New(), Channel: domain.ChannelD2C, LocationCode: storeCode,
			Items: []app.OrderItem{{SKUCode: skuCode, Qty: 1}},
		})
		require.NoError(t, err)
		ids = append(ids, o.ID)
	}

	_, err := f.svc.SyncPOS(ctx, syncOf(sale(f.serials[0], f.serials[1])))
	require.NoError(t, err) // 1 unit left, 3 reserved: two orders must lose

	statusOf := func(id int64) string {
		var s string
		require.NoError(t, f.pool.QueryRow(ctx, `SELECT status FROM orders WHERE id = $1`, id).Scan(&s))
		return s
	}
	assert.Equal(t, "PENDING_PAYMENT", statusOf(ids[0]), "the oldest order keeps its stock")
	assert.Equal(t, "NEEDS_REALLOCATION", statusOf(ids[1]))
	assert.Equal(t, "NEEDS_REALLOCATION", statusOf(ids[2]))
	assert.Equal(t, 1, testdb.Reserved(t, f.pool, f.skuID, f.storeID))
	testdb.AssertInvariants(t, f.pool)
}

// T10: two different transactions sell the same serial at the same time. One is valid; the other
// is still recorded, flagged, and takes no stock.
func TestIntegration_PosSync_SerialSoldTwice(t *testing.T) {
	f := newStore(t, 3, 0)
	var mu sync.Mutex
	var all []app.PosResult

	runTogether(2, func(int) {
		results, err := f.svc.SyncPOS(context.Background(), syncOf(sale(f.serials[0])))
		if assert.NoError(t, err) {
			mu.Lock()
			all = append(all, results...)
			mu.Unlock()
		}
	})

	require.Len(t, all, 2)
	got := statuses(all)
	assert.ElementsMatch(t, []domain.PosStatus{domain.PosAccepted, domain.PosAcceptedWithIssues}, got)
	for _, r := range all {
		if r.Status == domain.PosAcceptedWithIssues {
			assert.Equal(t, domain.IssueNotInStock, r.Issues[0].Issue)
			assert.Equal(t, 1, r.Issues[0].LineNo)
		}
	}
	assert.Equal(t, 2, f.onHand(t), "stock was taken once")
	assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM anomaly WHERE type = 'POS_SERIAL_MISMATCH'`))
	assert.Equal(t, 2, f.count(t, `SELECT COUNT(*) FROM pos_transaction`), "both sales are recorded")
	testdb.AssertInvariants(t, f.pool)
}

func TestIntegration_PosSync_RejectsAndIssues(t *testing.T) {
	f := newStore(t, 5, 0)
	ctx := context.Background()

	good := sale(f.serials[0])
	reusedID := sale(f.serials[1])
	_, err := f.svc.SyncPOS(ctx, syncOf(reusedID))
	require.NoError(t, err)

	t.Run("same id with a different payload is rejected", func(t *testing.T) {
		changed := reusedID
		changed.TotalAmount++
		res, err := f.svc.SyncPOS(ctx, syncOf(changed))
		require.NoError(t, err)
		assert.Equal(t, domain.PosRejected, res[0].Status)
		assert.Equal(t, string(domain.RejectIDReused), res[0].Error)
	})

	t.Run("same device and local_seq with a new id is rejected", func(t *testing.T) {
		clash := sale(f.serials[2])
		clash.LocalSeq = reusedID.LocalSeq
		res, err := f.svc.SyncPOS(ctx, syncOf(clash))
		require.NoError(t, err)
		assert.Equal(t, domain.PosRejected, res[0].Status)
		assert.Equal(t, string(domain.RejectLocalSeqReused), res[0].Error)
	})

	t.Run("an unknown sku and a malformed transaction are rejected, the rest still goes through", func(t *testing.T) {
		unknownSKU := sale(f.serials[3])
		unknownSKU.Items[0].SKUCode = "NOPE"
		malformed := sale(f.serials[4])
		malformed.PaymentMethod = "BARTER"

		res, err := f.svc.SyncPOS(ctx, syncOf(unknownSKU, malformed, good))
		require.NoError(t, err)
		require.Len(t, res, 3)
		byID := map[uuid.UUID]app.PosResult{}
		for _, r := range res {
			byID[r.ID] = r
		}
		assert.Equal(t, string(domain.RejectUnknownSKU), byID[unknownSKU.ID].Error)
		assert.Equal(t, string(domain.RejectInvalidPayload), byID[malformed.ID].Error)
		assert.Equal(t, domain.PosAccepted, byID[good.ID].Status, "a bad transaction never blocks a good one")
	})

	t.Run("a code that does not exist is a recorded sale with an issue and takes no stock", func(t *testing.T) {
		before := f.onHand(t)
		res, err := f.svc.SyncPOS(ctx, syncOf(sale("MG99999999999")))
		require.NoError(t, err)
		assert.Equal(t, domain.PosAcceptedWithIssues, res[0].Status)
		assert.Equal(t, domain.IssueSerialNotFound, res[0].Issues[0].Issue)
		assert.Equal(t, before, f.onHand(t))
	})

	t.Run("results come back in local_seq order", func(t *testing.T) {
		later, earlier := sale(f.serials[4]), sale()
		earlier.Items = nil // invalid, but only its position matters here
		later.LocalSeq = 1_000_001
		earlier.LocalSeq = 1_000_000
		res, err := f.svc.SyncPOS(ctx, syncOf(later, earlier))
		require.NoError(t, err)
		assert.Equal(t, earlier.ID, res[0].ID)
		assert.Equal(t, later.ID, res[1].ID)
	})

	testdb.AssertInvariants(t, f.pool)
}

func TestIntegration_PosSync_UnknownStore(t *testing.T) {
	f := newStore(t, 1, 0)
	testdb.InsertWarehouse(t, f.pool, "WH-JKT")
	ctx := context.Background()

	for _, code := range []string{"NOPE", "WH-JKT"} { // missing, and not an OWN_STORE
		in := syncOf(sale(f.serials[0]))
		in.StoreCode = code
		_, err := f.svc.SyncPOS(ctx, in)
		assert.ErrorIs(t, err, domain.ErrStoreNotFound, code)
		assert.ErrorIs(t, f.svc.Heartbeat(ctx, code, "dev"), domain.ErrStoreNotFound, code)
	}
}

// T14: cancels and POS syncs hit the same store at the same time. Only the end state is checked:
// no deadlock error anywhere, no more reserved than physically there, invariants intact.
func TestIntegration_PosSyncVsCancel(t *testing.T) {
	f := newStore(t, 60, 0)
	ctx := context.Background()

	orderIDs := make([]int64, 50)
	for i := range orderIDs {
		o, _, err := f.svc.CreateOrder(ctx, app.CreateOrderInput{
			IdempotencyKey: uuid.New(), Channel: domain.ChannelD2C, LocationCode: storeCode,
			Items: []app.OrderItem{{SKUCode: skuCode, Qty: 1}},
		})
		require.NoError(t, err)
		orderIDs[i] = o.ID
	}
	require.Equal(t, 50, testdb.Reserved(t, f.pool, f.skuID, f.storeID))

	runTogether(100, func(i int) {
		if i < 50 {
			_, err := f.svc.CancelOrder(ctx, orderIDs[i])
			assert.NoError(t, err, "cancel %d", i) // any 40P01 would show up here
			return
		}
		results, err := f.svc.SyncPOS(ctx, syncOf(sale(f.serials[i-50])))
		if assert.NoError(t, err, "sync %d", i) {
			assert.Equal(t, domain.PosAccepted, results[0].Status)
		}
	})

	assert.Equal(t, 10, f.onHand(t), "50 of 60 units were sold at the cashier")
	assert.LessOrEqual(t, testdb.Reserved(t, f.pool, f.skuID, f.storeID), f.onHand(t), "never more reserved than there is")
	testdb.AssertInvariants(t, f.pool)
}

func TestIntegration_Heartbeat_DeviceBelongsToOneStore(t *testing.T) {
	f := newStore(t, 1, 0)
	testdb.InsertStore(t, f.pool, "STORE-SBY-01", 0)
	ctx := context.Background()

	require.NoError(t, f.svc.Heartbeat(ctx, storeCode, "pos-a"))
	require.NoError(t, f.svc.Heartbeat(ctx, storeCode, "pos-a"), "the same device again is fine")
	assert.ErrorIs(t, f.svc.Heartbeat(ctx, "STORE-SBY-01", "pos-a"), domain.ErrDeviceStoreMismatch)
}

// T22: store stock is sold online only to D2C, and only while every active POS device is online.
func TestIntegration_StoreStockRules(t *testing.T) {
	f := newStore(t, 5, 0)
	ctx := context.Background()
	order := func(channel domain.Channel) error {
		_, _, err := f.svc.CreateOrder(ctx, app.CreateOrderInput{
			IdempotencyKey: uuid.New(), Channel: channel, LocationCode: storeCode,
			Items: []app.OrderItem{{SKUCode: skuCode, Qty: 1}},
		})
		return err
	}

	require.NoError(t, f.svc.Heartbeat(ctx, storeCode, "pos-a"))
	require.NoError(t, f.svc.Heartbeat(ctx, storeCode, "pos-b"))
	_, err := f.pool.Exec(ctx, `UPDATE pos_device SET last_heartbeat_at = now() - interval '10 minutes' WHERE id = 'pos-b'`)
	require.NoError(t, err)

	assert.ErrorIs(t, order(domain.ChannelD2C), domain.ErrInsufficientStock, "one active device is stale")

	require.NoError(t, f.svc.Heartbeat(ctx, storeCode, "pos-b"))
	assert.NoError(t, order(domain.ChannelD2C), "every device is online")
	assert.ErrorIs(t, order(domain.ChannelMarketplace), domain.ErrInsufficientStock, "store stock is not for marketplace orders")

	_, err = f.pool.Exec(ctx, `UPDATE pos_device SET last_heartbeat_at = now() - interval '10 minutes', active = false WHERE id = 'pos-b'`)
	require.NoError(t, err)
	assert.NoError(t, order(domain.ChannelD2C), "a deactivated device is ignored")
}
