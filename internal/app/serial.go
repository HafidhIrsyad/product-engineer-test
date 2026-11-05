package app

import (
	"context"
	"fmt"
	"time"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// transitionSerial moves a serial along its lifecycle and writes the history, in the caller's
// transaction (ARCHITECTURE.md §9.3). Two layers guard it:
//   - domain.CanTransition rejects a move that is not in the lifecycle (ErrInvalidTransition);
//   - the conditional UPDATE ... WHERE status = from rejects a stale read, when another process
//     changed the status first (ErrStaleStatus).
//
// It returns first_verified_at, which is set by the first move to VERIFIED.
func transitionSerial(ctx context.Context, q *store.Queries, serialID int64, from, to domain.SerialStatus, locationID *int64, refType, ref string) (*time.Time, error) {
	if !domain.CanTransition(from, to) {
		return nil, fmt.Errorf("%w: %s → %s", domain.ErrInvalidTransition, from, to)
	}
	at, err := q.TransitionSerial(ctx, store.TransitionSerialParams{ID: serialID, FromStatus: from, ToStatus: to})
	if store.IsNotFound(err) {
		return nil, fmt.Errorf("%w: expected %s", domain.ErrStaleStatus, from)
	}
	if err != nil {
		return nil, err
	}
	err = q.InsertSerialEvent(ctx, store.InsertSerialEventParams{
		SerialID: serialID, FromStatus: &from, ToStatus: to, LocationID: locationID, RefType: refType, RefID: &ref,
	})
	return at, err
}
