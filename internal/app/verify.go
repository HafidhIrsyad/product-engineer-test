package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
)

// VerifyInput is one consumer scan (ARCHITECTURE.md §10.1). Lat and Lng come together or not at all.
type VerifyInput struct {
	Code     domain.SerialCode `json:"code"`
	DeviceID string            `json:"device_id"`
	Lat      *float64          `json:"lat"`
	Lng      *float64          `json:"lng"`
}

// VerifyView is what the consumer sees. Which rule raised a warning is never shown: a forger
// must not learn the rules. That detail is only in the anomaly table.
type VerifyView struct {
	Verdict         domain.Verdict `json:"verdict"`
	Message         string         `json:"message,omitempty"`
	Product         *VerifyProduct `json:"product,omitempty"`
	Batch           *VerifyBatch   `json:"batch,omitempty"`
	FirstVerifiedAt *time.Time     `json:"first_verified_at,omitempty"`
	ScanCount       int            `json:"scan_count,omitempty"`
}

type VerifyProduct struct {
	SKU  string `json:"sku"`
	Name string `json:"name"`
}

type VerifyBatch struct {
	Code      string `json:"code"`
	ExpiresOn string `json:"expires_on"`
}

const (
	messageNotFound = "Kode ini tidak ditemukan. Produk mungkin tidak asli. Hubungi layanan pelanggan."
	messageWarning  = "Produk ini asli, tetapi ada hal yang tidak biasa pada kodenya (misalnya sudah pernah dipindai di tempat lain). Jika Anda baru membeli produk ini, hubungi layanan pelanggan."
)

// Verify checks a scanned code and records the scan (§10.2, §16.2). Scanning never changes
// stock and never changes the status of a unit in a shelf: only the first scan after the sale
// moves SOLD to VERIFIED.
func (s *Service) Verify(ctx context.Context, in VerifyInput) (VerifyView, error) {
	var view VerifyView
	err := s.store.InTx(ctx, func(q *store.Queries) error {
		row, err := q.GetSerialForVerify(ctx, string(in.Code))
		if store.IsNotFound(err) {
			view = VerifyView{Verdict: domain.VerdictNotFound, Message: messageNotFound}
			return insertScan(ctx, q, nil, in, view.Verdict) // R0: only logged in scan_event
		}
		if err != nil {
			return err
		}

		scanInput, err := scanContext(ctx, q, row, in)
		if err != nil {
			return err
		}
		result := domain.Evaluate(scanInput.ScanInput)
		for _, a := range result.Anomalies {
			if err := insertScanAnomaly(ctx, q, row, in, a); err != nil {
				return err
			}
		}

		firstVerified := row.FirstVerifiedAt
		if row.Status == domain.SerialSold {
			at, err := transitionSerial(ctx, q, row.ID, domain.SerialSold, domain.SerialVerified, &row.LocationID, "SCAN", string(in.Code))
			if err != nil {
				return err
			}
			firstVerified = at
		}

		view = VerifyView{
			Verdict:         result.Verdict,
			Product:         &VerifyProduct{SKU: row.SkuCode, Name: row.SkuName},
			Batch:           &VerifyBatch{Code: row.BatchCode, ExpiresOn: row.ExpiresOn.Format(time.DateOnly)},
			FirstVerifiedAt: firstVerified,
			ScanCount:       int(scanInput.PriorScans) + 1,
		}
		if result.Verdict == domain.VerdictGenuineWithWarning {
			view.Message = messageWarning
		}
		return insertScan(ctx, q, &row.ID, in, result.Verdict)
	})
	return view, err
}

// scanContext gathers what the pure rules need: the previous scan from another device,
// how many devices scanned this code, and the distributor's region.
func scanContext(ctx context.Context, q *store.Queries, row store.GetSerialForVerifyRow, in VerifyInput) (verifyScan, error) {
	out := verifyScan{ScanInput: domain.ScanInput{Status: row.Status, At: time.Now()}}
	if in.Lat != nil && in.Lng != nil {
		out.Location = &domain.Point{Lat: *in.Lat, Lng: *in.Lng}
	}
	if row.RegionLat != nil && row.RegionLng != nil && row.RegionRadiusKm != nil {
		out.Region = &domain.Region{Center: domain.Point{Lat: *row.RegionLat, Lng: *row.RegionLng}, RadiusKm: *row.RegionRadiusKm}
	}

	prev, err := q.GetPreviousOtherDeviceScan(ctx, store.GetPreviousOtherDeviceScanParams{SerialID: &row.ID, DeviceID: in.DeviceID})
	switch {
	case err == nil:
		out.Previous = &domain.PreviousScan{At: prev.ScannedAt}
		if prev.Lat != nil && prev.Lng != nil {
			out.Previous.Location = &domain.Point{Lat: *prev.Lat, Lng: *prev.Lng}
		}
	case !store.IsNotFound(err):
		return out, err
	}

	others, err := q.CountOtherScanDevices(ctx, store.CountOtherScanDevicesParams{SerialID: &row.ID, DeviceID: in.DeviceID})
	if err != nil {
		return out, err
	}
	out.DistinctDevices = int(others) + 1 // this device counts too
	if out.PriorScans, err = q.CountScans(ctx, &row.ID); err != nil {
		return out, err
	}
	return out, nil
}

// verifyScan is the rule input plus the scan count shown to the consumer.
type verifyScan struct {
	domain.ScanInput
	PriorScans int32
}

func insertScan(ctx context.Context, q *store.Queries, serialID *int64, in VerifyInput, v domain.Verdict) error {
	return q.InsertScanEvent(ctx, store.InsertScanEventParams{
		SerialID: serialID, RawCode: string(in.Code), DeviceID: in.DeviceID, Lat: in.Lat, Lng: in.Lng, Verdict: string(v),
	})
}

func insertScanAnomaly(ctx context.Context, q *store.Queries, row store.GetSerialForVerifyRow, in VerifyInput, a domain.AnomalyType) error {
	details, err := json.Marshal(map[string]any{"device_id": in.DeviceID, "lat": in.Lat, "lng": in.Lng})
	if err != nil {
		return err
	}
	refType, ref := "SCAN", string(in.Code)
	return q.InsertAnomaly(ctx, store.InsertAnomalyParams{
		Type: string(a), SerialID: &row.ID, LocationID: &row.LocationID, PartnerID: row.PartnerID,
		RefType: &refType, RefID: &ref, Details: details,
	})
}
