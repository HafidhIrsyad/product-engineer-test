package app

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

const maxReasonLen = 200

// Quarantine sets one unit apart (QC failed, damaged, suspected). It stops counting as available.
// A unit that is broken stays broken even when an order was promised it: if that leaves more
// promised than physically good, the same rule as the POS conflict applies (an order loses its stock).
func (s *Service) Quarantine(ctx context.Context, code domain.SerialCode, reason string) (SerialTrace, error) {
	if reason == "" || len(reason) > maxReasonLen {
		return SerialTrace{}, fmt.Errorf("%w: reason is required (at most %d characters)", domain.ErrInvalidInput, maxReasonLen)
	}
	var row store.GetSerialForUpdateRow
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		var err error
		if row, err = lockUnit(ctx, q, code); err != nil {
			return err
		}
		if _, err := transitionSerial(ctx, q, row.ID, row.Status, domain.SerialQuarantine, &row.LocationID, "QC", reason); err != nil {
			return err
		}
		return adjustQuarantine(ctx, q, row, 1, domain.MovementQuarantine)
	})
	if err != nil {
		return SerialTrace{}, err
	}
	// The unit is already set apart. If resolving fails, the periodic ResolveOversold sweep finishes it.
	if err := s.resolveOversell(ctx, row.LocationID, row.SkuID); err != nil {
		zerolog.Ctx(ctx).Error().Err(err).Msg("oversell resolution after quarantine failed")
	}
	return s.TraceSerial(ctx, code)
}

// ReleaseQuarantine puts a checked unit back: IN_WAREHOUSE in a central warehouse, IN_STOCK elsewhere.
func (s *Service) ReleaseQuarantine(ctx context.Context, code domain.SerialCode) (SerialTrace, error) {
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		row, err := lockUnit(ctx, q, code)
		if err != nil {
			return err
		}
		if row.Status != domain.SerialQuarantine {
			return fmt.Errorf("%w: %s is not quarantined", domain.ErrInvalidTransition, row.Status)
		}
		to := domain.SerialInStock
		if row.LocationType == domain.LocationCentralWarehouse {
			to = domain.SerialInWarehouse
		}
		if _, err := transitionSerial(ctx, q, row.ID, row.Status, to, &row.LocationID, "QC", "released"); err != nil {
			return err
		}
		return adjustQuarantine(ctx, q, row, -1, domain.MovementReleaseQuarantine)
	})
	if err != nil {
		return SerialTrace{}, err
	}
	return s.TraceSerial(ctx, code)
}

func lockUnit(ctx context.Context, q *store.Queries, code domain.SerialCode) (store.GetSerialForUpdateRow, error) {
	row, err := q.GetSerialForUpdate(ctx, string(code))
	if store.IsNotFound(err) {
		return row, fmt.Errorf("%w: %s", domain.ErrSerialNotFound, code)
	}
	if err != nil {
		return row, err
	}
	if row.Level != domain.LevelUnit {
		return row, fmt.Errorf("%w: only a single unit can be quarantined, not a %s", domain.ErrInvalidInput, row.Level)
	}
	return row, nil
}
