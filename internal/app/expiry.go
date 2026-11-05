package app

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	ids, err := s.store.ListExpiredOrderIDs(ctx)
	if err != nil {
		return 0, err
	}
	expired := 0
	var firstErr error
	for _, id := range ids {
		done, err := s.expireOne(ctx, id)
		if err != nil {
			// One failing order must not keep every later order's stock reserved.
			if firstErr == nil {
				firstErr = fmt.Errorf("expire order %d: %w", id, err)
			}
			continue
		}
		if done {
			expired++
		}
	}
	return expired, firstErr
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
			if err := s.ResolveOversold(ctx); err != nil && ctx.Err() == nil {
				log.Error().Err(err).Msg("oversell sweep failed")
			}
		}
	}
}
