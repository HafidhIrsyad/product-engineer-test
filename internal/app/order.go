package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// OrderItem is a SKU and quantity, both in the request and in the order view.
type OrderItem struct {
	SKUCode string `json:"sku_code"`
	Qty     int32  `json:"qty"`
}

// CreateOrderInput is the validated request. IdempotencyKey comes from the header.
type CreateOrderInput struct {
	IdempotencyKey uuid.UUID      `json:"-"`
	Channel        domain.Channel `json:"channel"`
	LocationCode   string         `json:"location_code"`
	Items          []OrderItem    `json:"items"`
}

// OrderView is what the API returns for an order.
type OrderView struct {
	ID           int64              `json:"id"`
	Channel      domain.Channel     `json:"channel"`
	LocationCode string             `json:"location_code"`
	Status       domain.OrderStatus `json:"status"`
	ExpiresAt    time.Time          `json:"expires_at"`
	Items        []OrderItem        `json:"items"`
}

// errReplay: another request with the same Idempotency-Key committed first.
var errReplay = errors.New("idempotency key already used")

// CreateOrder reserves stock and creates the order in one transaction (§12.2, §16.3).
// created is false when the Idempotency-Key was already used: the existing order is returned.
func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (view OrderView, created bool, err error) {
	if id, err := s.store.GetOrderIDByIdempotencyKey(ctx, in.IdempotencyKey); err == nil {
		view, err := loadView(ctx, s.store.Queries, id)
		return view, false, err
	} else if !store.IsNotFound(err) {
		return OrderView{}, false, err
	}

	location, err := s.store.GetLocationByCode(ctx, in.LocationCode)
	if store.IsNotFound(err) {
		return OrderView{}, false, fmt.Errorf("%w: %q", domain.ErrUnknownLocation, in.LocationCode)
	}
	if err != nil {
		return OrderView{}, false, err
	}
	lines, err := s.stockLines(ctx, in.Items)
	if err != nil {
		return OrderView{}, false, err
	}

	var orderID int64
	err = s.store.InTx(ctx, func(q *store.Queries) error {
		orderID, err = q.InsertOrder(ctx, store.InsertOrderParams{
			IdempotencyKey: in.IdempotencyKey, Channel: string(in.Channel), LocationID: location.ID,
		})
		if store.IsNotFound(err) {
			return errReplay // 0 rows: ON CONFLICT DO NOTHING
		}
		if err != nil {
			return err
		}
		for _, l := range lines {
			if err := q.InsertOrderItem(ctx, store.InsertOrderItemParams{OrderID: orderID, SkuID: l.skuID, Qty: l.qty}); err != nil {
				return err
			}
		}
		return reserve(ctx, q, orderID, location.ID, in.Channel, lines)
	})
	if errors.Is(err, errReplay) {
		id, err := s.store.GetOrderIDByIdempotencyKey(ctx, in.IdempotencyKey)
		if err != nil {
			return OrderView{}, false, err
		}
		view, err := loadView(ctx, s.store.Queries, id)
		return view, false, err
	}
	if err != nil {
		return OrderView{}, false, err
	}
	view, err = loadView(ctx, s.store.Queries, orderID)
	return view, true, err
}

// stockLines resolves SKU codes and sorts the lines by sku_id, so every transaction
// locks stock rows in the same order (§12.4).
func (s *Service) stockLines(ctx context.Context, items []OrderItem) ([]stockLine, error) {
	codes := make([]string, len(items))
	for i, it := range items {
		codes[i] = it.SKUCode
	}
	rows, err := s.store.GetSKUsByCodes(ctx, codes)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]int64, len(rows))
	for _, r := range rows {
		ids[r.Code] = r.ID
	}
	lines := make([]stockLine, len(items))
	for i, it := range items {
		id, ok := ids[it.SKUCode]
		if !ok {
			return nil, fmt.Errorf("%w: %q", domain.ErrUnknownSKU, it.SKUCode)
		}
		lines[i] = stockLine{skuID: id, qty: it.Qty}
	}
	slices.SortFunc(lines, func(a, b stockLine) int { return int(a.skuID - b.skuID) })
	return lines, nil
}

// GetOrder returns an order as the API shows it.
func (s *Service) GetOrder(ctx context.Context, id int64) (OrderView, error) {
	return loadView(ctx, s.store.Queries, id)
}

// PayOrder marks an order paid (mock payment). One conditional UPDATE decides, so it cannot
// pay an order that expired or was cancelled meanwhile (§12.7).
func (s *Service) PayOrder(ctx context.Context, id int64) (OrderView, error) {
	n, err := s.store.PayOrder(ctx, id)
	if err != nil {
		return OrderView{}, err
	}
	view, err := loadView(ctx, s.store.Queries, id)
	if err != nil {
		return OrderView{}, err
	}
	if n == 1 {
		return view, nil
	}
	switch view.Status {
	case domain.OrderPaid:
		return view, nil // idempotent
	case domain.OrderExpired, domain.OrderPendingPayment: // pending but past expires_at, not yet swept
		return OrderView{}, domain.ErrOrderExpired
	default:
		return OrderView{}, domain.ErrInvalidOrderStatus
	}
}

// CancelOrder cancels an order and releases its stock, under a row lock (§12.5).
// Cancelling twice, or racing with itself, releases stock only once.
func (s *Service) CancelOrder(ctx context.Context, id int64) (OrderView, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		o, err := q.LockOrder(ctx, id)
		if store.IsNotFound(err) {
			return domain.ErrOrderNotFound
		}
		if err != nil {
			return err
		}
		switch o.Status {
		case domain.OrderCancelled, domain.OrderExpired:
			return nil // nothing to do; stock was already released
		case domain.OrderFulfilled:
			return domain.ErrOrderNotCancellable
		case domain.OrderNeedsReallocation:
			// Stock was released when the order lost the conflict with the POS.
			return q.SetOrderStatus(ctx, store.SetOrderStatusParams{ID: id, Status: domain.OrderCancelled})
		}
		items, err := q.GetOrderItems(ctx, id)
		if err != nil {
			return err
		}
		lines := make([]stockLine, len(items)) // already sorted by sku_id
		for i, it := range items {
			lines[i] = stockLine{skuID: it.SkuID, qty: it.Qty}
		}
		if err := q.SetOrderStatus(ctx, store.SetOrderStatusParams{ID: id, Status: domain.OrderCancelled}); err != nil {
			return err
		}
		return release(ctx, q, id, o.LocationID, lines, "ORDER")
	})
	if err != nil {
		return OrderView{}, err
	}
	return loadView(ctx, s.store.Queries, id)
}

func loadView(ctx context.Context, q *store.Queries, id int64) (OrderView, error) {
	o, err := q.GetOrderView(ctx, id)
	if store.IsNotFound(err) {
		return OrderView{}, domain.ErrOrderNotFound
	}
	if err != nil {
		return OrderView{}, err
	}
	rows, err := q.GetOrderItems(ctx, id)
	if err != nil {
		return OrderView{}, err
	}
	items := make([]OrderItem, len(rows))
	for i, r := range rows {
		items[i] = OrderItem{SKUCode: r.SkuCode, Qty: r.Qty}
	}
	return OrderView{
		ID: o.ID, Channel: domain.Channel(o.Channel), LocationCode: o.LocationCode,
		Status: o.Status, ExpiresAt: o.ExpiresAt, Items: items,
	}, nil
}
