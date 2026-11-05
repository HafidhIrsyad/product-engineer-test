package app

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// ExpireDue releases the reservation of every unpaid order past expires_at (ARCHITECTURE.md §12.7).
// Each order gets its own transaction, like cancel: a conditional UPDATE decides, so an order that
// was paid or cancelled meanwhile is skipped. It returns how many orders expired.
func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	ids, err := s.store.ListExpiredOrderIDs(ctx)
	if err != nil {
		return 0, err
	}
	expired := 0
	for _, id := range ids {
		done, err := s.expireOne(ctx, id)
		if err != nil {
			return expired, err
		}
		if done {
			expired++
		}
	}
	return expired, nil
}

func (s *Service) expireOne(ctx context.Context, id int64) (bool, error) {
	done := false
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		locationID, err := q.ExpireOrder(ctx, id)
		if store.IsNotFound(err) {
			return nil // no longer waiting for payment
		}
		if err != nil {
			return err
		}
		items, err := q.GetOrderItems(ctx, id) // sorted by sku_id
		if err != nil {
			return err
		}
		lines := make([]stockLine, len(items))
		for i, it := range items {
			lines[i] = stockLine{skuID: it.SkuID, qty: it.Qty}
		}
		done = true
		return release(ctx, q, id, locationID, lines, "ORDER_EXPIRY")
	})
	return done && err == nil, err
}

// RunExpirySweeper runs ExpireDue every interval until ctx ends. Run it in one goroutine of a
// single API instance. A failed round is logged and the next one runs normally; correctness does
// not depend on the sweeper, because PayOrder itself refuses an order past expires_at.
func (s *Service) RunExpirySweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	log := zerolog.Ctx(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.ExpireDue(ctx)
			if err != nil && ctx.Err() == nil {
				log.Error().Err(err).Msg("expiry sweeper round failed")
			}
			if n > 0 {
				log.Info().Int("orders", n).Msg("expired unpaid orders")
			}
		}
	}
}
