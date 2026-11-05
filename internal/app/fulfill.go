package app

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

type FulfillInput struct {
	OrderID     int64    `json:"-"`
	SerialCodes []string `json:"serial_codes"`
}

// FulfillOrder turns the promise of an order (a count) into the specific units a picker scanned.
// Each unit becomes SOLD, and the stock leaves with its reservation. Sending the same units again
// for an order that is already fulfilled is a no-op.
func (s *Service) FulfillOrder(ctx context.Context, in FulfillInput) (OrderView, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		o, err := q.LockOrder(ctx, in.OrderID)
		if store.IsNotFound(err) {
			return domain.ErrOrderNotFound
		}
		if err != nil {
			return err
		}
		if o.Status == domain.OrderFulfilled {
			return sameUnitsAlreadyFulfilled(ctx, q, in)
		}
		if o.Status != domain.OrderPaid {
			return fmt.Errorf("%w: only a PAID order can be fulfilled, this one is %s", domain.ErrInvalidOrderStatus, o.Status)
		}

		items, err := q.GetOrderItems(ctx, in.OrderID)
		if err != nil {
			return err
		}
		units, err := lockUnitsForOrder(ctx, q, in.SerialCodes, o.LocationID)
		if err != nil {
			return err
		}
		perSKU := map[int64]int32{}
		for _, u := range units {
			perSKU[u.SkuID]++
		}
		lines := make([]stockLine, len(items)) // sorted by sku_id
		for i, it := range items {
			if perSKU[it.SkuID] != it.Qty {
				return fmt.Errorf("%w: sku %s needs exactly %d units, got %d", domain.ErrInvalidInput, it.SkuCode, it.Qty, perSKU[it.SkuID])
			}
			delete(perSKU, it.SkuID)
			lines[i] = stockLine{skuID: it.SkuID, qty: it.Qty}
		}
		if len(perSKU) > 0 {
			return fmt.Errorf("%w: some units are of a product that is not in the order", domain.ErrInvalidInput)
		}

		ids := make([]int64, len(units))
		for i, u := range units {
			if _, err := transitionSerial(ctx, q, u.ID, u.Status, domain.SerialSold, &o.LocationID, "ORDER", strconv.FormatInt(in.OrderID, 10)); err != nil {
				return err
			}
			ids[i] = u.ID
		}
		if err := q.SetSerialsOrder(ctx, store.SetSerialsOrderParams{OrderID: &in.OrderID, Ids: ids}); err != nil {
			return err
		}
		if err := fulfillStock(ctx, q, in.OrderID, o.LocationID, lines); err != nil {
			return err
		}
		return q.SetOrderStatus(ctx, store.SetOrderStatusParams{ID: in.OrderID, Status: domain.OrderFulfilled})
	})
	if err != nil {
		return OrderView{}, err
	}
	return loadView(ctx, s.store.Queries, in.OrderID)
}

// lockUnitsForOrder locks the scanned units in one query (id order) and checks each is a unit
// that can be sold from the order's location.
func lockUnitsForOrder(ctx context.Context, q *store.Queries, codes []string, locationID int64) ([]store.LockSerialsByCodesRow, error) {
	if len(codes) == 0 {
		return nil, fmt.Errorf("%w: serial_codes must not be empty", domain.ErrInvalidInput)
	}
	unique := slices.Clone(codes)
	slices.Sort(unique)
	if len(slices.Compact(unique)) != len(codes) {
		return nil, fmt.Errorf("%w: a serial code is listed twice", domain.ErrInvalidInput)
	}
	rows, err := q.LockSerialsByCodes(ctx, codes)
	if err != nil {
		return nil, err
	}
	if len(rows) != len(codes) {
		return nil, fmt.Errorf("%w: one or more serial codes are unknown", domain.ErrSerialNotFound)
	}
	for _, r := range rows {
		if r.Level != domain.LevelUnit || r.LocationID != locationID {
			return nil, fmt.Errorf("%w: %s is not a unit at the order's location", domain.ErrInvalidInput, r.Code)
		}
	}
	return rows, nil
}

func sameUnitsAlreadyFulfilled(ctx context.Context, q *store.Queries, in FulfillInput) error {
	have, err := q.ListOrderSerialCodes(ctx, &in.OrderID)
	if err != nil {
		return err
	}
	want := slices.Clone(in.SerialCodes)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		return fmt.Errorf("%w: the order is already fulfilled with other units", domain.ErrInvalidOrderStatus)
	}
	return nil
}

func fulfillStock(ctx context.Context, q *store.Queries, orderID, locationID int64, lines []stockLine) error {
	for _, l := range lines {
		n, err := q.FulfillStock(ctx, store.FulfillStockParams{Qty: l.qty, SkuID: l.skuID, LocationID: locationID})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("fulfill: reservation for sku %d at location %d is missing: invariant I2 broken", l.skuID, locationID)
		}
		m := movement{locationID: locationID, deltaOnHand: -l.qty, deltaReserved: -l.qty, reason: domain.MovementSaleOnline, refType: "ORDER", refID: strconv.FormatInt(orderID, 10)}
		if err := ledger(ctx, q, l, m); err != nil {
			return err
		}
	}
	return nil
}
