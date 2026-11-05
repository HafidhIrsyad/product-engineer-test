package app

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

type stockLine struct {
	skuID int64
	qty   int32
}

// movement identifies one ledger entry: what changed, why, and because of which record.
type movement struct {
	locationID      int64
	deltaOnHand     int32
	deltaReserved   int32
	deltaQuarantine int32
	reason          domain.MovementReason
	refType         string
	refID           string
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
		n, err := q.ReleaseStock(ctx, store.ReleaseStockParams{Qty: l.qty, SkuID: l.skuID, LocationID: locationID})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("release: no stock_balance row for sku %d at location %d", l.skuID, locationID)
		}
		m := movement{locationID: locationID, deltaReserved: -l.qty, reason: domain.MovementRelease, refType: refType, refID: strconv.FormatInt(orderID, 10)}
		if err := ledger(ctx, q, l, m); err != nil {
			return err
		}
	}
	return nil
}

func sellPOS(ctx context.Context, q *store.Queries, txnID uuid.UUID, locationID int64, lines []stockLine) error {
	for _, l := range lines {
		n, err := q.DecreaseOnHand(ctx, store.DecreaseOnHandParams{Qty: l.qty, SkuID: l.skuID, LocationID: locationID})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: sale: no stock_balance row for sku %d at location %d", domain.ErrDataInconsistent, l.skuID, locationID)
		}
		m := movement{locationID: locationID, deltaOnHand: -l.qty, reason: domain.MovementSalePOS, refType: "POS_TXN", refID: txnID.String()}
		if err := ledger(ctx, q, l, m); err != nil {
			return err
		}
	}
	return nil
}

// shipOut takes stock out of a location. Stock already promised to an order cannot leave.
func shipOut(ctx context.Context, q *store.Queries, container string, locationID int64, lines []stockLine) error {
	for _, l := range lines {
		n, err := q.ShipOutStock(ctx, store.ShipOutStockParams{Qty: l.qty, SkuID: l.skuID, LocationID: locationID})
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrInsufficientStock
		}
		m := movement{locationID: locationID, deltaOnHand: -l.qty, reason: domain.MovementShipOut, refType: "SHIPMENT", refID: container}
		if err := ledger(ctx, q, l, m); err != nil {
			return err
		}
	}
	return nil
}

// receiveStock adds stock to a location.
func receiveStock(ctx context.Context, q *store.Queries, container string, locationID int64, lines []stockLine) error {
	for _, l := range lines {
		if err := q.ReceiveStock(ctx, store.ReceiveStockParams{Qty: l.qty, SkuID: l.skuID, LocationID: locationID}); err != nil {
			return err
		}
		m := movement{locationID: locationID, deltaOnHand: l.qty, reason: domain.MovementReceive, refType: "SHIPMENT", refID: container}
		if err := ledger(ctx, q, l, m); err != nil {
			return err
		}
	}
	return nil
}

func ledger(ctx context.Context, q *store.Queries, l stockLine, m movement) error {
	return q.InsertStockMovement(ctx, store.InsertStockMovementParams{
		SkuID: l.skuID, LocationID: m.locationID, DeltaOnHand: m.deltaOnHand, DeltaReserved: m.deltaReserved, DeltaQuarantine: m.deltaQuarantine,
		Reason: m.reason, RefType: m.refType, RefID: m.refID,
	})
}
