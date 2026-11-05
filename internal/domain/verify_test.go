package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

var (
	jakarta = domain.Point{Lat: -6.2088, Lng: 106.8456}
	bandung = domain.Point{Lat: -6.9175, Lng: 107.6191}
	bogor   = domain.Point{Lat: -6.5950, Lng: 106.8166}
	medan   = domain.Point{Lat: 3.5952, Lng: 98.6722}
	london  = domain.Point{Lat: 51.5074, Lng: -0.1278}
	paris   = domain.Point{Lat: 48.8566, Lng: 2.3522}
)

// T13: haversine against known distances.
func TestHaversineKm(t *testing.T) {
	assert.InDelta(t, 343.5, domain.HaversineKm(london, paris), 2, "London–Paris is about 343 km")
	assert.InDelta(t, 120, domain.HaversineKm(jakarta, bandung), 10, "Jakarta–Bandung is about 120 km")
	assert.InDelta(t, 1420, domain.HaversineKm(jakarta, medan), 40, "Jakarta–Medan is about 1,400 km")
	assert.Zero(t, domain.HaversineKm(jakarta, jakarta))
	assert.InDelta(t, domain.HaversineKm(jakarta, medan), domain.HaversineKm(medan, jakarta), 1e-9, "symmetric")
}

// T11: anomaly rules R1–R3, table-driven.
func TestEvaluate_Anomalies(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	prev := func(ago time.Duration, loc *domain.Point) *domain.PreviousScan {
		return &domain.PreviousScan{At: now.Add(-ago), Location: loc}
	}
	medanRegion := &domain.Region{Center: medan, RadiusKm: 150}

	tests := []struct {
		name string
		in   domain.ScanInput
		want []domain.AnomalyType
	}{
		{
			name: "clean scan of a sold unit",
			in:   domain.ScanInput{Status: domain.SerialSold, At: now, Location: &jakarta, DistinctDevices: 1},
		},
		{
			name: "R1: Jakarta → Medan in 30 minutes is impossible",
			in: domain.ScanInput{Status: domain.SerialSold, At: now, Location: &medan,
				Previous: prev(30*time.Minute, &jakarta), DistinctDevices: 2},
			want: []domain.AnomalyType{domain.AnomalyImpossibleTravel},
		},
		{
			name: "R1: Jakarta → Medan in 5 hours is normal (about 284 km/h)",
			in: domain.ScanInput{Status: domain.SerialSold, At: now, Location: &medan,
				Previous: prev(5*time.Hour, &jakarta), DistinctDevices: 2},
		},
		{
			name: "R1: far away at the very same instant",
			in: domain.ScanInput{Status: domain.SerialSold, At: now, Location: &medan,
				Previous: prev(0, &jakarta), DistinctDevices: 2},
			want: []domain.AnomalyType{domain.AnomalyImpossibleTravel},
		},
		{
			name: "R1: under 100 km is ignored however fast (Jakarta → Bogor, about 43 km, in 1 minute)",
			in: domain.ScanInput{Status: domain.SerialSold, At: now, Location: &bogor,
				Previous: prev(time.Minute, &jakarta), DistinctDevices: 2},
		},
		{
			name: "R1: just over 100 km (Jakarta → Bandung, about 116 km) in 1 minute is impossible",
			in: domain.ScanInput{Status: domain.SerialSold, At: now, Location: &bandung,
				Previous: prev(time.Minute, &jakarta), DistinctDevices: 2},
			want: []domain.AnomalyType{domain.AnomalyImpossibleTravel},
		},
		{
			name: "R1: skipped when the previous scan had no location",
			in: domain.ScanInput{Status: domain.SerialSold, At: now, Location: &medan,
				Previous: prev(time.Minute, nil), DistinctDevices: 2},
		},
		{
			name: "R1: skipped when this scan has no location",
			in: domain.ScanInput{Status: domain.SerialSold, At: now,
				Previous: prev(time.Minute, &jakarta), DistinctDevices: 2},
		},
		{
			name: "R2: unit still in the warehouse",
			in:   domain.ScanInput{Status: domain.SerialInWarehouse, At: now, Location: &jakarta, DistinctDevices: 1},
			want: []domain.AnomalyType{domain.AnomalyScanBeforeRelease},
		},
		{
			name: "R2: produced, in transit and quarantined units too",
			in:   domain.ScanInput{Status: domain.SerialQuarantine, At: now, Location: &jakarta, DistinctDevices: 1},
			want: []domain.AnomalyType{domain.AnomalyScanBeforeRelease},
		},
		{
			name: "R2 does not fire for a unit in stock at a partner",
			in:   domain.ScanInput{Status: domain.SerialInStock, At: now, Location: &jakarta, DistinctDevices: 1},
		},
		{
			name: "R3: allocated to Medan, scanned in Jakarta",
			in: domain.ScanInput{Status: domain.SerialInStock, At: now, Location: &jakarta,
				Region: medanRegion, DistinctDevices: 1},
			want: []domain.AnomalyType{domain.AnomalyOutOfRegion},
		},
		{
			name: "R3: scanned inside the region",
			in: domain.ScanInput{Status: domain.SerialInStock, At: now, Location: &domain.Point{Lat: 3.7, Lng: 98.7},
				Region: medanRegion, DistinctDevices: 1},
		},
		{
			name: "R3: skipped without a location",
			in:   domain.ScanInput{Status: domain.SerialInStock, At: now, Region: medanRegion, DistinctDevices: 1},
		},
		{
			name: "R3: skipped when the serial has no distributor",
			in:   domain.ScanInput{Status: domain.SerialInStock, At: now, Location: &jakarta, DistinctDevices: 1},
		},
		{
			name: "several rules at once keep a fixed order R1, R2, R3",
			in: domain.ScanInput{Status: domain.SerialInTransit, At: now, Location: &medan,
				Previous: prev(time.Minute, &jakarta), Region: &domain.Region{Center: jakarta, RadiusKm: 10},
				DistinctDevices: 2},
			want: []domain.AnomalyType{domain.AnomalyImpossibleTravel, domain.AnomalyScanBeforeRelease, domain.AnomalyOutOfRegion},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, domain.Evaluate(tt.in).Anomalies)
		})
	}
}

// T18: combinations of rules and device counts → verdict (§10.4).
func TestDecideVerdict(t *testing.T) {
	r1 := []domain.AnomalyType{domain.AnomalyImpossibleTravel}
	tests := []struct {
		name      string
		found     bool
		anomalies []domain.AnomalyType
		devices   int
		want      domain.Verdict
	}{
		{"no rule fires", true, nil, 1, domain.VerdictGenuine},
		{"R1 fires", true, r1, 1, domain.VerdictGenuineWithWarning},
		{"R2 fires", true, []domain.AnomalyType{domain.AnomalyScanBeforeRelease}, 1, domain.VerdictGenuineWithWarning},
		{"R3 fires", true, []domain.AnomalyType{domain.AnomalyOutOfRegion}, 1, domain.VerdictGenuineWithWarning},
		{"scanned by 2 devices, no rule", true, nil, 2, domain.VerdictGenuine},
		{"scanned by 3 devices, no rule", true, nil, 3, domain.VerdictGenuineWithWarning},
		{"unknown code", false, nil, 0, domain.VerdictNotFound},
		{"unknown code wins over anything else", false, r1, 5, domain.VerdictNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, domain.DecideVerdict(tt.found, tt.anomalies, tt.devices))
		})
	}
}

func TestEvaluate_VerdictFollowsRules(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	clean := domain.Evaluate(domain.ScanInput{Status: domain.SerialSold, At: now, DistinctDevices: 2})
	assert.Equal(t, domain.VerdictGenuine, clean.Verdict)

	released := domain.Evaluate(domain.ScanInput{Status: domain.SerialProduced, At: now, DistinctDevices: 1})
	assert.Equal(t, domain.VerdictGenuineWithWarning, released.Verdict)
}
