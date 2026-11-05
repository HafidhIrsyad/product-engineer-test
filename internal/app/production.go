package app

import (
	"context"
	"fmt"
	"time"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

type CreateBatchInput struct {
	SKUCode      string `json:"sku_code"`
	BatchCode    string `json:"batch_code"`
	LocationCode string `json:"location_code"`
	ExpiresOn    string `json:"expires_on"`
	Pallets      int    `json:"pallets"`
}

type BatchView struct {
	Code      string       `json:"code"`
	SKU       string       `json:"sku"`
	ExpiresOn string       `json:"expires_on"`
	Pallets   []PalletView `json:"pallets"`
	Cartons   int          `json:"cartons"`
	Units     int          `json:"units"`
	Labels    []UnitLabel  `json:"unit_labels"`
}

type UnitLabel struct {
	SerialCode string `json:"serial_code"`
	InnerCode  string `json:"inner_code"`
}

type PalletView struct {
	Code    string   `json:"code"`
	Cartons []string `json:"cartons"`
}

const dateLayout = "2006-01-02"

// CreateBatch prints a production batch: pallets, cartons and units, each with a unique code.
// Everything starts as PRODUCED at the factory, so stock does not change.
func (s *Service) CreateBatch(ctx context.Context, in CreateBatchInput) (BatchView, error) {
	expires, err := time.Parse(dateLayout, in.ExpiresOn)
	if err != nil || !expires.After(time.Now().AddDate(0, 0, 1)) {
		return BatchView{}, fmt.Errorf("%w: expires_on must be a date at least a day ahead (YYYY-MM-DD)", domain.ErrInvalidInput)
	}

	var view BatchView
	err = s.store.InTx(ctx, func(q *store.Queries) error {
		sku, err := q.GetSKUByCode(ctx, in.SKUCode)
		if store.IsNotFound(err) {
			return fmt.Errorf("%w: %s", domain.ErrUnknownSKU, in.SKUCode)
		}
		if err != nil {
			return err
		}
		loc, err := q.GetLocationByCode(ctx, in.LocationCode)
		if store.IsNotFound(err) {
			return fmt.Errorf("%w: %s", domain.ErrUnknownLocation, in.LocationCode)
		}
		if err != nil {
			return err
		}
		if loc.Type != domain.LocationFactory {
			return fmt.Errorf("%w: a batch is produced at a FACTORY location", domain.ErrInvalidInput)
		}
		batchID, err := q.InsertBatch(ctx, store.InsertBatchParams{Code: in.BatchCode, SkuID: sku.ID, ExpiresOn: expires})
		if store.IsNotFound(err) {
			return fmt.Errorf("%w: %s", domain.ErrBatchExists, in.BatchCode)
		}
		if err != nil {
			return err
		}

		ids := serialIDs{sku: sku.ID, batch: batchID, location: loc.ID}
		pallets, err := insertLevel(ctx, q, ids, domain.LevelPallet, make([]int64, in.Pallets)) // no parents
		if err != nil {
			return err
		}
		cartons, err := insertLevel(ctx, q, ids, domain.LevelCarton, repeat(idsOf(pallets), int(sku.CartonsPerPallet)))
		if err != nil {
			return err
		}
		units, err := insertLevel(ctx, q, ids, domain.LevelUnit, repeat(idsOf(cartons), int(sku.UnitsPerCarton)))
		if err != nil {
			return err
		}
		labels, err := setInnerCodes(ctx, q, units)
		if err != nil {
			return err
		}
		if err := q.InsertBatchSerialEvents(ctx, store.InsertBatchSerialEventsParams{RefID: in.BatchCode, BatchID: batchID}); err != nil {
			return err
		}

		view = BatchView{Code: in.BatchCode, SKU: in.SKUCode, ExpiresOn: in.ExpiresOn, Cartons: len(cartons), Units: len(units)}
		view.Labels = labels // the only time the secret inner codes exist in clear: for the printer
		cartonsOf := map[int64][]string{}
		for _, c := range cartons {
			cartonsOf[*c.ParentID] = append(cartonsOf[*c.ParentID], c.Code)
		}
		for _, p := range pallets {
			view.Pallets = append(view.Pallets, PalletView{Code: p.Code, Cartons: cartonsOf[p.ID]})
		}
		return nil
	})
	return view, err
}

// setInnerCodes gives every unit a secret code under its seal. Only the hash is stored.
func setInnerCodes(ctx context.Context, q *store.Queries, units []store.InsertSerialsRow) ([]UnitLabel, error) {
	labels := make([]UnitLabel, len(units))
	hashes := make([][]byte, len(units))
	for i, u := range units {
		inner := domain.NewInnerCode()
		labels[i] = UnitLabel{SerialCode: u.Code, InnerCode: inner}
		hashes[i] = domain.HashInnerCode(inner)
	}
	return labels, q.SetInnerCodeHashes(ctx, store.SetInnerCodeHashesParams{Ids: idsOf(units), Hashes: hashes})
}

type serialIDs struct{ sku, batch, location int64 }

// insertLevel creates one serial per entry of parentIDs, with fresh random codes.
func insertLevel(ctx context.Context, q *store.Queries, ids serialIDs, level domain.SerialLevel, parentIDs []int64) ([]store.InsertSerialsRow, error) {
	return q.InsertSerials(ctx, store.InsertSerialsParams{
		Codes: newCodes(len(parentIDs)), ParentIds: parentIDs, Level: level,
		SkuID: ids.sku, BatchID: ids.batch, LocationID: ids.location,
	})
}

func idsOf(rows []store.InsertSerialsRow) []int64 {
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids
}

// repeat lists every id n times in a row: [1 2] x 2 = [1 1 2 2].
func repeat(ids []int64, n int) []int64 {
	out := make([]int64, 0, len(ids)*n)
	for _, id := range ids {
		for range n {
			out = append(out, id)
		}
	}
	return out
}

func newCodes(n int) []string {
	codes := make([]string, n)
	for i := range codes {
		codes[i] = string(domain.NewSerialCode())
	}
	return codes
}

type SerialTrace struct {
	Code            string              `json:"code"`
	Level           domain.SerialLevel  `json:"level"`
	Status          domain.SerialStatus `json:"status"`
	SKU             string              `json:"sku"`
	Batch           string              `json:"batch"`
	Location        string              `json:"location"`
	Partner         *string             `json:"partner,omitempty"`
	FirstVerifiedAt *time.Time          `json:"first_verified_at,omitempty"`
	Parents         []TraceParent       `json:"parents"`
	Units           *int32              `json:"units,omitempty"`
	History         []TraceEvent        `json:"history"`
}

type TraceParent struct {
	Code  string             `json:"code"`
	Level domain.SerialLevel `json:"level"`
}

type TraceEvent struct {
	From     *domain.SerialStatus `json:"from,omitempty"`
	To       domain.SerialStatus  `json:"to"`
	Location *string              `json:"location,omitempty"`
	RefType  string               `json:"ref_type"`
	RefID    *string              `json:"ref_id,omitempty"`
	At       time.Time            `json:"at"`
}

// TraceSerial shows where a unit, carton or pallet came from and where it has been.
func (s *Service) TraceSerial(ctx context.Context, code domain.SerialCode) (SerialTrace, error) {
	row, err := s.store.GetSerialTrace(ctx, string(code))
	if store.IsNotFound(err) {
		return SerialTrace{}, fmt.Errorf("%w: %s", domain.ErrSerialNotFound, code)
	}
	if err != nil {
		return SerialTrace{}, err
	}
	t := SerialTrace{
		Code: row.Code, Level: row.Level, Status: row.Status, SKU: row.SkuCode, Batch: row.BatchCode,
		Location: row.LocationCode, Partner: row.PartnerName, FirstVerifiedAt: row.FirstVerifiedAt,
		Parents: []TraceParent{}, History: []TraceEvent{},
	}
	ancestors, err := s.store.ListSerialAncestors(ctx, row.ID)
	if err != nil {
		return SerialTrace{}, err
	}
	for _, a := range ancestors {
		t.Parents = append(t.Parents, TraceParent{Code: a.Code, Level: a.Level})
	}
	if row.Level != domain.LevelUnit {
		n, err := s.store.CountUnitsUnder(ctx, &row.ID)
		if err != nil {
			return SerialTrace{}, err
		}
		t.Units = &n
	}
	events, err := s.store.ListSerialEvents(ctx, row.ID)
	if err != nil {
		return SerialTrace{}, err
	}
	for _, e := range events {
		t.History = append(t.History, TraceEvent{From: e.FromStatus, To: e.ToStatus, Location: e.LocationCode, RefType: e.RefType, RefID: e.RefID, At: e.CreatedAt})
	}
	return t, nil
}
