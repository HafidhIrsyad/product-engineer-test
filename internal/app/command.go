package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

type StockRow struct {
	Location        string              `json:"location"`
	LocationType    domain.LocationType `json:"location_type"`
	SKU             string              `json:"sku"`
	OnHand          int32               `json:"on_hand"`
	Reserved        int32               `json:"reserved"`
	Quarantine      int32               `json:"quarantine"`
	ATP             int32               `json:"atp"`
	Inbound         int32               `json:"inbound"`
	Sold14d         int64               `json:"sold_14d"`
	DaysOfInventory *float64            `json:"days_of_inventory"`
	RunoutWarning   bool                `json:"runout_warning"`
}

// StockOverview shows stock at every location in one place: what is physically there, what is
// promised, what is set apart, what can still be sold, and how long it will last.
func (s *Service) StockOverview(ctx context.Context, sku, location string) ([]StockRow, error) {
	rows, err := s.store.ListStockOverview(ctx, store.ListStockOverviewParams{SkuCode: optional(sku), LocationCode: optional(location)})
	if err != nil {
		return nil, err
	}
	out := make([]StockRow, len(rows))
	for i, r := range rows {
		days := domain.DaysOfInventory(r.OnHand, r.Quarantine, r.Sold14d)
		out[i] = StockRow{
			Location: r.LocationCode, LocationType: r.LocationType, SKU: r.SkuCode,
			OnHand: r.OnHand, Reserved: r.Reserved, Quarantine: r.Quarantine,
			ATP: r.OnHand - r.Reserved - r.Quarantine, Inbound: r.Inbound, Sold14d: r.Sold14d,
			DaysOfInventory: days, RunoutWarning: domain.RunoutWarning(days),
		}
	}
	return out, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type AnomalyView struct {
	ID       int64           `json:"id"`
	Type     string          `json:"type"`
	Serial   *string         `json:"serial_code,omitempty"`
	Location *string         `json:"location,omitempty"`
	Partner  *string         `json:"partner,omitempty"`
	RefType  *string         `json:"ref_type,omitempty"`
	RefID    *string         `json:"ref_id,omitempty"`
	Details  json.RawMessage `json:"details"`
	At       time.Time       `json:"at"`
}

type AnomalyFilter struct {
	Type    string
	Partner string
	Since   time.Time
}

var anomalyTypes = []domain.AnomalyType{
	domain.AnomalyImpossibleTravel, domain.AnomalyScanBeforeRelease, domain.AnomalyOutOfRegion,
	domain.AnomalyPosSerialMismatch, domain.AnomalyOverSold,
}

// Anomalies lists open anomalies: the early warning of the command center.
func (s *Service) Anomalies(ctx context.Context, f AnomalyFilter) ([]AnomalyView, error) {
	if f.Type != "" && !isAnomalyType(f.Type) {
		return nil, fmt.Errorf("%w: unknown anomaly type %q", domain.ErrInvalidInput, f.Type)
	}
	rows, err := s.store.ListAnomalies(ctx, store.ListAnomaliesParams{Type: optional(f.Type), PartnerName: optional(f.Partner), Since: f.Since})
	if err != nil {
		return nil, err
	}
	out := make([]AnomalyView, len(rows))
	for i, r := range rows {
		out[i] = AnomalyView{
			ID: r.ID, Type: r.Type, Serial: r.SerialCode, Location: r.LocationCode, Partner: r.PartnerName,
			RefType: r.RefType, RefID: r.RefID, Details: r.Details, At: r.CreatedAt,
		}
	}
	return out, nil
}

func isAnomalyType(s string) bool {
	for _, t := range anomalyTypes {
		if string(t) == s {
			return true
		}
	}
	return false
}

// A pallet is reported as leaking when at least this many of its units, and at least this share of
// the units scanned from it, were scanned outside the distributor's region.
const (
	leakageMinUnits = 3
	leakageMinRatio = 0.3
)

type LeakageRow struct {
	Distributor        string `json:"distributor"`
	Pallet             string `json:"pallet"`
	ScannedUnits       int32  `json:"scanned_units"`
	UnitsOutsideRegion int32  `json:"units_outside_region"`
}

// Leakage points at the pallets whose units show up outside the region they were allocated to,
// which names the distributor and the shipment without a physical inspection.
func (s *Service) Leakage(ctx context.Context) ([]LeakageRow, error) {
	rows, err := s.store.LeakageByPallet(ctx, store.LeakageByPalletParams{MinUnits: leakageMinUnits, MinRatio: leakageMinRatio})
	if err != nil {
		return nil, err
	}
	out := make([]LeakageRow, len(rows))
	for i, r := range rows {
		out[i] = LeakageRow{Distributor: r.Distributor, Pallet: r.Pallet, ScannedUnits: r.ScannedUnits, UnitsOutsideRegion: r.UnitsOutsideRegion}
	}
	return out, nil
}

type SellOutRow struct {
	Partner      string    `json:"partner"`
	Week         time.Time `json:"week"`
	ShippedUnits int32     `json:"shipped_units"`
	ScannedUnits int32     `json:"scanned_units"`
}

// SellOutSignal gives, per distributor and week, the units shipped and the units consumers scanned.
func (s *Service) SellOutSignal(ctx context.Context, from, to time.Time) ([]SellOutRow, error) {
	if !to.After(from) {
		return nil, fmt.Errorf("%w: to must be after from", domain.ErrInvalidInput)
	}
	rows, err := s.store.SellOutSignal(ctx, store.SellOutSignalParams{FromTime: from, ToTime: to})
	if err != nil {
		return nil, err
	}
	out := make([]SellOutRow, len(rows))
	for i, r := range rows {
		out[i] = SellOutRow{Partner: r.Partner, Week: r.Week, ShippedUnits: r.ShippedUnits, ScannedUnits: r.ScannedUnits}
	}
	return out, nil
}
