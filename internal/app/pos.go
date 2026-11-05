package app

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/google/uuid"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// SyncPOSInput is the body of POST /v1/pos/sync (ARCHITECTURE.md §13.2).
type SyncPOSInput struct {
	StoreCode    string          `json:"store_code"`
	DeviceID     string          `json:"device_id"`
	Transactions []domain.PosTxn `json:"transactions"`
}

// PosResult is the outcome of one transaction in a sync.
type PosResult struct {
	ID             uuid.UUID        `json:"id"`
	Status         domain.PosStatus `json:"status"`
	OriginalResult string           `json:"original_result,omitempty"` // DUPLICATE: how it was first processed
	Issues         []PosItemIssue   `json:"issues,omitempty"`
	Error          string           `json:"error,omitempty"` // REJECTED: why
}

// PosItemIssue is a scanned serial that was not a valid sale.
type PosItemIssue struct {
	LineNo int             `json:"line_no"`
	Issue  domain.PosIssue `json:"issue"`
}

// Heartbeat records that a POS device is online. A store is sold online only while every
// active device of the store has a fresh heartbeat (§13.6).
func (s *Service) Heartbeat(ctx context.Context, storeCode, deviceID string) error {
	loc, err := s.posStore(ctx, storeCode)
	if err != nil {
		return err
	}
	return s.touchDevice(ctx, loc.ID, deviceID)
}

// touchDevice records a fresh heartbeat. 0 rows means the device is registered at another store.
func (s *Service) touchDevice(ctx context.Context, storeID int64, deviceID string) error {
	n, err := s.store.TouchHeartbeat(ctx, store.TouchHeartbeatParams{DeviceID: deviceID, StoreID: storeID})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrDeviceStoreMismatch
	}
	return nil
}

// SyncPOS records offline cashier transactions (§13.3, §16.5). Two stages:
//   - Tx1, one transaction per POS transaction, in local_seq order: record the sale, mark serials
//     sold, take stock. A bad transaction never fails the batch.
//   - Tx2, afterwards, per SKU in the batch: resolve any oversell against online reservations.
//
// They are separate so every transaction follows the global lock order (§12.4).
// Results come back in local_seq order. A retried batch is safe: finished transactions become
// DUPLICATE, and Tx2 runs again for every SKU, including those of duplicates.
func (s *Service) SyncPOS(ctx context.Context, in SyncPOSInput) ([]PosResult, error) {
	loc, err := s.posStore(ctx, in.StoreCode)
	if err != nil {
		return nil, err
	}
	// A sync proves the device is online, and ties it to this store: a device belongs to one store,
	// and one that never sent a heartbeat is still counted when deciding if the store is online (§13.6).
	if err := s.touchDevice(ctx, loc.ID, in.DeviceID); err != nil {
		return nil, err
	}
	txns := slices.Clone(in.Transactions)
	slices.SortStableFunc(txns, func(a, b domain.PosTxn) int { return cmp.Compare(a.LocalSeq, b.LocalSeq) })

	results := make([]PosResult, 0, len(txns))
	touched := map[int64]bool{}
	for _, t := range txns {
		res, skuIDs, err := s.syncTxn(ctx, loc, in.DeviceID, t)
		if err != nil {
			return nil, fmt.Errorf("transaction %s: %w", t.ID, err)
		}
		results = append(results, res)
		for _, id := range skuIDs {
			touched[id] = true
		}
	}

	skuIDs := make([]int64, 0, len(touched))
	for id := range touched {
		skuIDs = append(skuIDs, id)
	}
	slices.Sort(skuIDs)
	for _, id := range skuIDs {
		if err := s.resolveOversell(ctx, loc.ID, id); err != nil {
			return nil, fmt.Errorf("resolve oversell for sku %d: %w", id, err)
		}
	}
	return results, nil
}

func (s *Service) posStore(ctx context.Context, code string) (store.Location, error) {
	loc, err := s.store.GetLocationByCode(ctx, code)
	if store.IsNotFound(err) || (err == nil && loc.Type != domain.LocationOwnStore) {
		return store.Location{}, fmt.Errorf("%w: %q", domain.ErrStoreNotFound, code)
	}
	return loc, err
}

func rejected(id uuid.UUID, why domain.PosRejection) PosResult {
	return PosResult{ID: id, Status: domain.PosRejected, Error: string(why)}
}

// syncTxn is Tx1 for one POS transaction. It returns the SKU ids involved, for Tx2.
func (s *Service) syncTxn(ctx context.Context, loc store.Location, deviceID string, t domain.PosTxn) (PosResult, []int64, error) {
	if err := t.Validate(); err != nil {
		return rejected(t.ID, domain.RejectInvalidPayload), nil, nil
	}
	skuIDs, err := s.skuIDsByCode(ctx, t.Items)
	if err != nil {
		return PosResult{}, nil, err
	}
	for _, it := range t.Items {
		if _, ok := skuIDs[it.SKUCode]; !ok {
			return rejected(t.ID, domain.RejectUnknownSKU), nil, nil
		}
	}

	var res PosResult
	err = s.store.InTx(ctx, func(q *store.Queries) error {
		hash := domain.PayloadHash(t)
		n, err := q.InsertPosTransaction(ctx, store.InsertPosTransactionParams{
			ID: t.ID, StoreID: loc.ID, DeviceID: deviceID, LocalSeq: t.LocalSeq, OccurredAt: t.OccurredAt,
			PayloadHash: hash, TotalAmount: t.TotalAmount, PaymentMethod: t.PaymentMethod,
		})
		if err != nil {
			return err
		}
		if n == 0 { // conflict: a retry, or a device bug
			existing, err := q.GetPosTransaction(ctx, t.ID)
			switch {
			case store.IsNotFound(err): // the id is new, so (device_id, local_seq) was reused
				res = rejected(t.ID, domain.RejectLocalSeqReused)
			case err != nil:
				return err
			case !bytes.Equal(existing.PayloadHash, hash):
				res = rejected(t.ID, domain.RejectIDReused)
			default:
				res = PosResult{ID: t.ID, Status: domain.PosDuplicate, OriginalResult: existing.Result}
			}
			return nil
		}
		res, err = recordSale(ctx, q, loc, t, skuIDs)
		return err
	})
	if err != nil {
		return PosResult{}, nil, err
	}
	ids := make([]int64, 0, len(skuIDs))
	for _, id := range skuIDs {
		ids = append(ids, id)
	}
	return res, ids, nil
}

func (s *Service) skuIDsByCode(ctx context.Context, items []domain.PosItem) (map[string]int64, error) {
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
	return ids, nil
}

// recordSale writes a new transaction: items, serial changes, anomalies, and stock.
// Lock order: serial, then stock_balance (§12.4). Stock is taken only for valid serials,
// so on_hand always equals the number of serials in the store (invariant I1).
func recordSale(ctx context.Context, q *store.Queries, loc store.Location, t domain.PosTxn, skuIDs map[string]int64) (PosResult, error) {
	codes := make([]string, len(t.Items))
	for i, it := range t.Items {
		codes[i] = it.SerialCode
	}
	locked, err := q.LockSerialsByCodes(ctx, codes) // one query, ordered by id
	if err != nil {
		return PosResult{}, err
	}
	serials := make(map[string]*store.LockSerialsByCodesRow, len(locked))
	for i := range locked {
		serials[locked[i].Code] = &locked[i]
	}

	res := PosResult{ID: t.ID, Status: domain.PosAccepted}
	sold := map[int64]int32{} // sku id → valid units
	for i, it := range t.Items {
		lineNo := i + 1
		row := serials[it.SerialCode]
		var snap *domain.SerialSnapshot
		if row != nil {
			snap = &domain.SerialSnapshot{Level: row.Level, SKUID: row.SkuID, LocationID: row.LocationID, Status: row.Status}
		}
		issue := domain.ClassifyPosItem(snap, skuIDs[it.SKUCode], loc.ID)

		var serialID *int64
		if row != nil {
			serialID = &row.ID
		}
		if issue == "" {
			if err := markSold(ctx, q, row, loc.ID, t.ID); err != nil {
				return PosResult{}, err
			}
			row.Status = domain.SerialSold // the same code scanned twice in one sale is not valid twice
			sold[row.SkuID]++
		} else {
			res.Status = domain.PosAcceptedWithIssues
			res.Issues = append(res.Issues, PosItemIssue{LineNo: lineNo, Issue: issue})
			if err := recordMismatch(ctx, q, loc.ID, t.ID, serialID, it.SerialCode, lineNo, issue); err != nil {
				return PosResult{}, err
			}
		}
		var issueText *string
		if issue != "" {
			text := string(issue)
			issueText = &text
		}
		if err := q.InsertPosTransactionItem(ctx, store.InsertPosTransactionItemParams{
			PosTransactionID: t.ID, LineNo: int32(lineNo), SerialCode: it.SerialCode, SerialID: serialID,
			SkuID: skuIDs[it.SKUCode], Price: it.Price, Issue: issueText,
		}); err != nil {
			return PosResult{}, err
		}
	}

	lines := make([]stockLine, 0, len(sold))
	for skuID, qty := range sold {
		lines = append(lines, stockLine{skuID: skuID, qty: qty})
	}
	slices.SortFunc(lines, func(a, b stockLine) int { return cmp.Compare(a.skuID, b.skuID) })
	if err := sellPOS(ctx, q, t.ID, loc.ID, lines); err != nil {
		return PosResult{}, err
	}
	if err := q.SetPosTransactionResult(ctx, store.SetPosTransactionResultParams{ID: t.ID, Result: string(res.Status)}); err != nil {
		return PosResult{}, err
	}
	return res, nil
}

func markSold(ctx context.Context, q *store.Queries, row *store.LockSerialsByCodesRow, storeID int64, txnID uuid.UUID) error {
	// A stale status is impossible while the row is locked: it would be a bug, so it surfaces as an error.
	_, err := transitionSerial(ctx, q, row.ID, domain.SerialInStock, domain.SerialSold, &storeID, "POS_TXN", txnID.String())
	return err
}

func recordMismatch(ctx context.Context, q *store.Queries, storeID int64, txnID uuid.UUID, serialID *int64, code string, lineNo int, issue domain.PosIssue) error {
	details, err := json.Marshal(map[string]any{"serial_code": code, "line_no": lineNo, "issue": issue})
	if err != nil {
		return err
	}
	refType, ref := "POS_TXN", txnID.String()
	return q.InsertAnomaly(ctx, store.InsertAnomalyParams{
		Type: string(domain.AnomalyPosSerialMismatch), SerialID: serialID, LocationID: &storeID,
		RefType: &refType, RefID: &ref, Details: details,
	})
}

// resolveOversell is Tx2 for one SKU of one store (§13.5). While more is reserved than is
// physically there, the newest open order holding the SKU loses its reservation. One
// transaction per order: lock order is advisory lock, then orders, then stock_balance.
func (s *Service) resolveOversell(ctx context.Context, storeID, skuID int64) error {
	for {
		more := false
		err := s.store.InTx(ctx, func(q *store.Queries) error {
			if err := q.LockStoreOversell(ctx, int32(storeID)); err != nil {
				return err
			}
			orderID, err := q.PickOversellVictim(ctx, store.PickOversellVictimParams{SkuID: skuID, LocationID: storeID})
			if store.IsNotFound(err) {
				return nil // no open order holds this SKU: nothing to take back
			}
			if err != nil {
				return err
			}
			more = true
			o, err := q.LockOrder(ctx, orderID)
			if err != nil {
				return err
			}
			if o.Status != domain.OrderPendingPayment && o.Status != domain.OrderPaid {
				return nil // cancelled or expired while we waited: pick again
			}
			// Only now, with the order locked, read the shortage and lock the stock row. A cancel
			// that committed before this point is seen, so an order is never sacrificed for nothing.
			shortfall, err := q.GetShortfall(ctx, store.GetShortfallParams{SkuID: skuID, LocationID: storeID})
			if store.IsNotFound(err) || (err == nil && shortfall <= 0) {
				more = false // nothing to resolve
				return nil
			}
			if err != nil {
				return err
			}
			return loseReservation(ctx, q, orderID, storeID, skuID, shortfall)
		})
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
}

// loseReservation releases every item of an order and marks it NEEDS_REALLOCATION.
func loseReservation(ctx context.Context, q *store.Queries, orderID, storeID, skuID int64, shortfall int32) error {
	items, err := q.GetOrderItems(ctx, orderID) // sorted by sku_id
	if err != nil {
		return err
	}
	lines := make([]stockLine, len(items))
	for i, it := range items {
		lines[i] = stockLine{skuID: it.SkuID, qty: it.Qty}
	}
	if err := release(ctx, q, orderID, storeID, lines, "OVERSOLD"); err != nil {
		return err
	}
	if err := q.SetOrderStatus(ctx, store.SetOrderStatusParams{ID: orderID, Status: domain.OrderNeedsReallocation}); err != nil {
		return err
	}
	details, err := json.Marshal(map[string]any{"sku_id": skuID, "shortfall": shortfall})
	if err != nil {
		return err
	}
	refType, ref := "ORDER", strconv.FormatInt(orderID, 10)
	return q.InsertAnomaly(ctx, store.InsertAnomalyParams{
		Type: string(domain.AnomalyOverSold), LocationID: &storeID, RefType: &refType, RefID: &ref, Details: details,
	})
}

// ResolveOversold finds store stock that is still over-reserved and resolves it (§13.5). Normally
// Tx2 does this right after a sync; this catches the case where a sync failed after Tx1 and the
// device never resent it. The sweeper calls it every round.
func (s *Service) ResolveOversold(ctx context.Context) error {
	rows, err := s.store.ListOversold(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, r := range rows {
		if err := s.resolveOversell(ctx, r.LocationID, r.SkuID); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("resolve oversell at location %d, sku %d: %w", r.LocationID, r.SkuID, err)
		}
	}
	return firstErr
}
