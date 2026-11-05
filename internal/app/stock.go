package app

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// stock.go is the ONLY place that changes stock_balance (ARCHITECTURE.md §11.3).
// Every change updates the counter and writes the same delta to the ledger, inside the
// caller's transaction. Lines must already be sorted by sku_id (lock order, §12.4).

type stockLine struct {
	skuID int64
	qty   int32
}

// movement identifies one ledger entry: what changed, why, and because of which record.
type movement struct {
	locationID    int64
	deltaOnHand   int32
	deltaReserved int32
	reason        domain.MovementReason
	refType       string
	refID         string
}

// reserve promises stock to an order. The conditional UPDATE decides; 0 rows = not enough.
func reserve(ctx context.Context, q *store.Queries, orderID, locationID int64, channel domain.Channel, lines []stockLine) error {
	for _, l := range lines {
		n, err := q.ReserveStock(ctx, store.ReserveStockParams{
			Qty: l.qty, SkuID: l.skuID, LocationID: locationID, Channel: string(channel),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrInsufficientStock
		}
		m := movement{locationID: locationID, deltaReserved: l.qty, reason: domain.MovementReserve, refType: "ORDER", refID: strconv.FormatInt(orderID, 10)}
		if err := ledger(ctx, q, l, m); err != nil {
			return err
		}
	}
	return nil
}

// release gives reserved stock back (cancel, expiry, lost conflict).
func release(ctx context.Context, q *store.Queries, orderID, locationID int64, lines []stockLine, refType string) error {
	for _, l := range lines {
		if _, err := q.ReleaseStock(ctx, store.ReleaseStockParams{Qty: l.qty, SkuID: l.skuID, LocationID: locationID}); err != nil {
			return err
		}
		m := movement{locationID: locationID, deltaReserved: -l.qty, reason: domain.MovementRelease, refType: refType, refID: strconv.FormatInt(orderID, 10)}
		if err := ledger(ctx, q, l, m); err != nil {
			return err
		}
	}
	return nil
}

// sellPOS takes physical units out of a store for a cashier sale. The units are real,
// so there is no availability check: a shortage against reservations is resolved afterwards (§13.5).
func sellPOS(ctx context.Context, q *store.Queries, txnID uuid.UUID, locationID int64, lines []stockLine) error {
	for _, l := range lines {
		n, err := q.DecreaseOnHand(ctx, store.DecreaseOnHandParams{Qty: l.qty, SkuID: l.skuID, LocationID: locationID})
		if err != nil {
			return err
		}
		if n == 0 {
			// A valid serial is IN_STOCK here, so a stock_balance row must exist (invariant I1).
			return domain.ErrInsufficientStock
		}
		m := movement{locationID: locationID, deltaOnHand: -l.qty, reason: domain.MovementSalePOS, refType: "POS_TXN", refID: txnID.String()}
		if err := ledger(ctx, q, l, m); err != nil {
			return err
		}
	}
	return nil
}

func ledger(ctx context.Context, q *store.Queries, l stockLine, m movement) error {
	return q.InsertStockMovement(ctx, store.InsertStockMovementParams{
		SkuID: l.skuID, LocationID: m.locationID, DeltaOnHand: m.deltaOnHand, DeltaReserved: m.deltaReserved,
		Reason: m.reason, RefType: m.refType, RefID: m.refID,
	})
}
