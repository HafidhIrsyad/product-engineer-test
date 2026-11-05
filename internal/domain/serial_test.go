package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

// T3: only the transitions of ARCHITECTURE.md §9.2 are allowed, out of all 7 × 7 pairs.
func TestCanTransition_AllPairs(t *testing.T) {
	type pair struct{ from, to domain.SerialStatus }
	// Written out by hand from §9.2, independent of the map under test.
	allowed := map[pair]bool{
		{domain.SerialProduced, domain.SerialInWarehouse}:   true,
		{domain.SerialInWarehouse, domain.SerialInTransit}:  true,
		{domain.SerialInWarehouse, domain.SerialSold}:       true,
		{domain.SerialInWarehouse, domain.SerialQuarantine}: true,
		{domain.SerialInTransit, domain.SerialInStock}:      true,
		{domain.SerialInStock, domain.SerialInTransit}:      true,
		{domain.SerialInStock, domain.SerialSold}:           true,
		{domain.SerialInStock, domain.SerialVerified}:       true,
		{domain.SerialInStock, domain.SerialQuarantine}:     true,
		{domain.SerialSold, domain.SerialVerified}:          true,
		{domain.SerialQuarantine, domain.SerialInWarehouse}: true,
		{domain.SerialQuarantine, domain.SerialInStock}:     true,
	}

	require.Len(t, domain.SerialStatuses, 7)
	for _, from := range domain.SerialStatuses {
		for _, to := range domain.SerialStatuses {
			assert.Equal(t, allowed[pair{from, to}], domain.CanTransition(from, to), "%s → %s", from, to)
		}
	}
}

func TestCanTransition_ExamplesFromSpec(t *testing.T) {
	assert.False(t, domain.CanTransition(domain.SerialProduced, domain.SerialSold), "PRODUCED → SOLD is rejected")
	for _, to := range domain.SerialStatuses {
		assert.False(t, domain.CanTransition(domain.SerialVerified, to), "VERIFIED → %s is rejected", to)
	}
}

func TestParseSerialCode(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    domain.SerialCode
		wantErr bool
	}{
		{name: "valid", raw: "MG7K2Q9XHD4PZ", want: "MG7K2Q9XHD4PZ"},
		{name: "normalized", raw: "  mg7k2q9xhd4pz\n", want: "MG7K2Q9XHD4PZ"},
		{name: "too short", raw: "MG7K2Q", wantErr: true},
		{name: "wrong prefix", raw: "XX7K2Q9XHD4PZ", wantErr: true},
		{name: "character outside Crockford base32 (I)", raw: "MG7K2Q9XHD4PI", wantErr: true},
		{name: "empty", raw: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseSerialCode(tt.raw)
			if tt.wantErr {
				require.ErrorIs(t, err, domain.ErrInvalidInput)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// T24: generated codes are valid, random and never repeat.
func TestNewSerialCode(t *testing.T) {
	seen := map[domain.SerialCode]bool{}
	for range 5000 {
		code := domain.NewSerialCode()
		parsed, err := domain.ParseSerialCode(string(code))
		require.NoError(t, err, "a generated code must pass the same check a scan does")
		assert.Equal(t, code, parsed)
		assert.False(t, seen[code], "duplicate code %s", code)
		seen[code] = true
	}
}
