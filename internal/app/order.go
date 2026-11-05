package app

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
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

func (s *Service) CreateOrder(ctx context.Context, in CreateOrderInput) (view OrderView, created bool, err error) {
	hash, err := orderHash(in)
	if err != nil {
		return OrderView{}, false, err
	}
	if view, found, err := s.replay(ctx, in.IdempotencyKey, hash); found || err != nil {
		return view, false, err
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
			IdempotencyKey: in.IdempotencyKey, RequestHash: hash, Channel: string(in.Channel), LocationID: location.ID,
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
		view, _, err := s.replay(ctx, in.IdempotencyKey, hash)
		return view, false, err
	}
	if err != nil {
		return OrderView{}, false, err
	}
	view, err = loadView(ctx, s.store.Queries, orderID)
	return view, true, err
}

// replay returns the order an Idempotency-Key already created. The same key with a different
// request is a client bug, and is refused.
func (s *Service) replay(ctx context.Context, key uuid.UUID, hash []byte) (OrderView, bool, error) {
	existing, err := s.store.GetOrderByIdempotencyKey(ctx, key)
	if store.IsNotFound(err) {
		return OrderView{}, false, nil
	}
	if err != nil {
		return OrderView{}, false, err
	}
	if !bytes.Equal(existing.RequestHash, hash) {
		return OrderView{}, false, domain.ErrIdempotencyKeyReused
	}
	view, err := loadView(ctx, s.store.Queries, existing.ID)
	return view, true, err
}

// orderHash identifies a request by what it asks for, whatever the order of its items.
func orderHash(in CreateOrderInput) ([]byte, error) {
	items := slices.Clone(in.Items)
	slices.SortFunc(items, func(a, b OrderItem) int { return cmp.Compare(a.SKUCode, b.SKUCode) })
	raw, err := json.Marshal(struct {
		Channel  domain.Channel
		Location string
		Items    []OrderItem
	}{in.Channel, in.LocationCode, items})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return sum[:], nil
}

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
	slices.SortFunc(lines, func(a, b stockLine) int { return cmp.Compare(a.skuID, b.skuID) })
	return lines, nil
}

// GetOrder returns an order as the API shows it.
func (s *Service) GetOrder(ctx context.Context, id int64) (OrderView, error) {
	return loadView(ctx, s.store.Queries, id)
}

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

// ReallocateOrder gives an order that lost its stock a new location. It comes back as it was:
// PAID if the customer had paid, otherwise waiting for payment with a fresh deadline.
func (s *Service) ReallocateOrder(ctx context.Context, id int64, locationCode string) (OrderView, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		o, err := q.LockOrder(ctx, id)
		if store.IsNotFound(err) {
			return domain.ErrOrderNotFound
		}
		if err != nil {
			return err
		}
		if o.Status != domain.OrderNeedsReallocation {
			return fmt.Errorf("%w: only a NEEDS_REALLOCATION order can be reallocated, this one is %s", domain.ErrInvalidOrderStatus, o.Status)
		}
		dest, err := q.GetLocationByCode(ctx, locationCode)
		if store.IsNotFound(err) {
			return fmt.Errorf("%w: %q", domain.ErrUnknownLocation, locationCode)
		}
		if err != nil {
			return err
		}
		view, err := q.GetOrderView(ctx, id)
		if err != nil {
			return err
		}
		items, err := q.GetOrderItems(ctx, id)
		if err != nil {
			return err
		}
		lines := make([]stockLine, len(items)) // already sorted by sku_id
		for i, it := range items {
			lines[i] = stockLine{skuID: it.SkuID, qty: it.Qty}
		}
		if err := reserve(ctx, q, id, dest.ID, domain.Channel(view.Channel), lines); err != nil {
			return err
		}
		status := domain.OrderPendingPayment
		if o.PaidAt != nil {
			status = domain.OrderPaid
		}
		return q.ReallocateOrder(ctx, store.ReallocateOrderParams{ID: id, LocationID: dest.ID, Status: status})
	})
	if err != nil {
		return OrderView{}, err
	}
	return loadView(ctx, s.store.Queries, id)
}
