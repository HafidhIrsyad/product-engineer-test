package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

func (f fixture) unitCodes(t *testing.T) []string {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `SELECT code FROM serial WHERE level = 'UNIT' ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		codes = append(codes, c)
	}
	return codes
}

// T31: a quarantined unit stops being available, and comes back when it is released.
func TestIntegration_Quarantine_ChangesAvailability(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 5})
	ctx := context.Background()
	codes := f.unitCodes(t)
	one := app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}

	_, _, err := f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 4}))
	require.NoError(t, err) // 1 free unit left

	trace, err := f.svc.Quarantine(ctx, domain.SerialCode(codes[0]), "seal broken")
	require.NoError(t, err)
	assert.Equal(t, domain.SerialQuarantine, trace.Status)
	assert.Equal(t, 1, f.count(t, `SELECT quarantine FROM stock_balance`))
	assert.Equal(t, 5, f.count(t, `SELECT on_hand FROM stock_balance`), "a quarantined unit is still physically there")
	testdb.AssertInvariants(t, f.pool)

	_, _, err = f.svc.CreateOrder(ctx, input(uuid.New(), one))
	assert.ErrorIs(t, err, domain.ErrInsufficientStock, "the last unit is quarantined")

	trace, err = f.svc.ReleaseQuarantine(ctx, domain.SerialCode(codes[0]))
	require.NoError(t, err)
	assert.Equal(t, domain.SerialInWarehouse, trace.Status, "a central warehouse takes it back as IN_WAREHOUSE")
	assert.Equal(t, 0, f.count(t, `SELECT quarantine FROM stock_balance`))
	_, _, err = f.svc.CreateOrder(ctx, input(uuid.New(), one))
	assert.NoError(t, err)
	testdb.AssertInvariants(t, f.pool)

	t.Run("refused", func(t *testing.T) {
		_, err := f.svc.Quarantine(ctx, domain.SerialCode(codes[1]), "")
		assert.ErrorIs(t, err, domain.ErrInvalidInput, "a reason is required")
		_, err = f.svc.Quarantine(ctx, domain.NewSerialCode(), "x")
		assert.ErrorIs(t, err, domain.ErrSerialNotFound)
		_, err = f.svc.ReleaseQuarantine(ctx, domain.SerialCode(codes[1]))
		assert.ErrorIs(t, err, domain.ErrInvalidTransition, "only a quarantined unit can be released")
		_, err = f.pool.Exec(ctx, `UPDATE serial SET status = 'SOLD' WHERE code = $1`, codes[2])
		require.NoError(t, err)
		_, err = f.svc.Quarantine(ctx, domain.SerialCode(codes[2]), "x")
		assert.ErrorIs(t, err, domain.ErrInvalidTransition, "a sold unit is not in stock any more")
	})
}

// T32: a unit that turns out broken while promised to an order: the physical fact wins, as with the POS.
func TestIntegration_Quarantine_PromisedUnit(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 2})
	ctx := context.Background()
	older, _, err := f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}))
	require.NoError(t, err)
	newer, _, err := f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}))
	require.NoError(t, err)

	_, err = f.svc.Quarantine(ctx, domain.SerialCode(f.unitCodes(t)[0]), "damaged in transit")
	require.NoError(t, err)

	assert.Equal(t, "PENDING_PAYMENT", f.orderStatus(t, older.ID))
	assert.Equal(t, "NEEDS_REALLOCATION", f.orderStatus(t, newer.ID))
	assert.Equal(t, 1, f.count(t, `SELECT reserved FROM stock_balance`))
	assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM anomaly WHERE type = 'OVERSOLD'`))
	testdb.AssertInvariants(t, f.pool)
}
