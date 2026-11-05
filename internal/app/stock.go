package app

import (
	"context"
	"strconv"

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
		if err := ledger(ctx, q, l, locationID, l.qty, domain.MovementReserve, "ORDER", orderID); err != nil {
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
		if err := ledger(ctx, q, l, locationID, -l.qty, domain.MovementRelease, refType, orderID); err != nil {
			return err
		}
	}
	return nil
}

func ledger(ctx context.Context, q *store.Queries, l stockLine, locationID int64, deltaReserved int32, reason domain.MovementReason, refType string, refID int64) error {
	return q.InsertStockMovement(ctx, store.InsertStockMovementParams{
		SkuID: l.skuID, LocationID: locationID, DeltaReserved: deltaReserved,
		Reason: reason, RefType: refType, RefID: strconv.FormatInt(refID, 10),
	})
}
