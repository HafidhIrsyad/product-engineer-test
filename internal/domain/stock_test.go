package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
)

// T40: days of inventory and the runout warning.
func TestDaysOfInventory(t *testing.T) {
	assert.Nil(t, domain.DaysOfInventory(100, 0, 0), "no sales: no rate, so no estimate")

	days := domain.DaysOfInventory(100, 0, 140) // 10 a day
	require.NotNil(t, days)
	assert.InDelta(t, 10, *days, 0.001)

	days = domain.DaysOfInventory(100, 30, 140) // quarantined units do not count as stock
	require.NotNil(t, days)
	assert.InDelta(t, 7, *days, 0.001)

	for name, tc := range map[string]struct {
		onHand int32
		sold   int64
		warn   bool
	}{
		"plenty of stock":   {onHand: 200, sold: 140, warn: false},
		"just enough":       {onHand: 100, sold: 140, warn: false}, // exactly 10 days
		"below the warning": {onHand: 99, sold: 140, warn: true},
		"nothing sold":      {onHand: 1, sold: 0, warn: false},
	} {
		assert.Equal(t, tc.warn, domain.RunoutWarning(domain.DaysOfInventory(tc.onHand, 0, tc.sold)), name)
	}
}
