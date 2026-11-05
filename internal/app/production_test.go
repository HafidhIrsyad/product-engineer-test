package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

func batchInput(pallets int) app.CreateBatchInput {
	return app.CreateBatchInput{
		SKUCode: "MGN-PROT-500", BatchCode: "B2611-99", LocationCode: "FACTORY-01",
		ExpiresOn: time.Now().AddDate(1, 0, 0).Format("2006-01-02"), Pallets: pallets,
	}
}

func (f fixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, f.pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

// T25: a batch prints every level with unique codes, correctly linked, and leaves stock alone.
func TestIntegration_CreateBatch(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 0})
	testdb.InsertFactory(t, f.pool, "FACTORY-01")
	ctx := context.Background()

	view, err := f.svc.CreateBatch(ctx, batchInput(2))
	require.NoError(t, err)

	// MGN-PROT-500 packs 12 units per carton and 40 cartons per pallet.
	assert.Len(t, view.Pallets, 2)
	assert.Equal(t, 80, view.Cartons)
	assert.Equal(t, 960, view.Units)
	for _, p := range view.Pallets {
		assert.Len(t, p.Cartons, 40)
	}

	assert.Equal(t, 2, f.count(t, `SELECT COUNT(*) FROM serial WHERE level = 'PALLET' AND parent_id IS NULL`))
	assert.Equal(t, 80, f.count(t, `SELECT COUNT(*) FROM serial c JOIN serial p ON p.id = c.parent_id AND p.level = 'PALLET' WHERE c.level = 'CARTON'`))
	assert.Equal(t, 960, f.count(t, `SELECT COUNT(*) FROM serial u JOIN serial c ON c.id = u.parent_id AND c.level = 'CARTON' WHERE u.level = 'UNIT'`))
	assert.Equal(t, 1042, f.count(t, `SELECT COUNT(DISTINCT code) FROM serial`), "every code is unique")
	assert.Equal(t, 1042, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'PRODUCED'`))
	assert.Equal(t, 1042, f.count(t, `SELECT COUNT(*) FROM serial_event WHERE ref_type = 'PACKING' AND to_status = 'PRODUCED'`))
	assert.Equal(t, 0, f.count(t, `SELECT COALESCE(SUM(on_hand), 0) FROM stock_balance`), "PRODUCED units are not stock yet")
	testdb.AssertInvariants(t, f.pool)

	t.Run("the same batch code twice is a conflict", func(t *testing.T) {
		_, err := f.svc.CreateBatch(ctx, batchInput(1))
		assert.ErrorIs(t, err, domain.ErrBatchExists)
		assert.Equal(t, 1042, f.count(t, `SELECT COUNT(*) FROM serial`), "nothing was added")
	})

	t.Run("bad input", func(t *testing.T) {
		in := batchInput(1)
		in.BatchCode = "B-OTHER"

		in.SKUCode = "NOPE"
		_, err := f.svc.CreateBatch(ctx, in)
		assert.ErrorIs(t, err, domain.ErrUnknownSKU)

		in.SKUCode, in.LocationCode = "MGN-PROT-500", "NOPE"
		_, err = f.svc.CreateBatch(ctx, in)
		assert.ErrorIs(t, err, domain.ErrUnknownLocation)

		in.LocationCode = "WH-JKT"
		_, err = f.svc.CreateBatch(ctx, in)
		assert.ErrorIs(t, err, domain.ErrInvalidInput, "a batch is printed at a factory")

		in.LocationCode, in.ExpiresOn = "FACTORY-01", "2020-01-01"
		_, err = f.svc.CreateBatch(ctx, in)
		assert.ErrorIs(t, err, domain.ErrInvalidInput, "already expired")
	})
}

func TestIntegration_TraceSerial(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 0})
	testdb.InsertFactory(t, f.pool, "FACTORY-01")
	ctx := context.Background()
	view, err := f.svc.CreateBatch(ctx, batchInput(1))
	require.NoError(t, err)

	pallet, err := f.svc.TraceSerial(ctx, domain.SerialCode(view.Pallets[0].Code))
	require.NoError(t, err)
	assert.Equal(t, domain.LevelPallet, pallet.Level)
	assert.Empty(t, pallet.Parents)
	require.NotNil(t, pallet.Units)
	assert.EqualValues(t, 480, *pallet.Units)

	carton, err := f.svc.TraceSerial(ctx, domain.SerialCode(view.Pallets[0].Cartons[0]))
	require.NoError(t, err)
	assert.Equal(t, []app.TraceParent{{Code: view.Pallets[0].Code, Level: domain.LevelPallet}}, carton.Parents)
	assert.EqualValues(t, 12, *carton.Units)

	var unitCode string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT code FROM serial WHERE level = 'UNIT' AND parent_id = (SELECT id FROM serial WHERE code = $1)`, view.Pallets[0].Cartons[0]).Scan(&unitCode))
	unit, err := f.svc.TraceSerial(ctx, domain.SerialCode(unitCode))
	require.NoError(t, err)
	assert.Equal(t, domain.SerialProduced, unit.Status)
	assert.Equal(t, "B2611-99", unit.Batch)
	assert.Equal(t, "FACTORY-01", unit.Location)
	assert.Equal(t, []string{view.Pallets[0].Cartons[0], view.Pallets[0].Code}, []string{unit.Parents[0].Code, unit.Parents[1].Code}, "nearest container first")
	assert.Nil(t, unit.Units)
	require.Len(t, unit.History, 1)
	assert.Equal(t, "PACKING", unit.History[0].RefType)

	_, err = f.svc.TraceSerial(ctx, domain.NewSerialCode())
	assert.ErrorIs(t, err, domain.ErrSerialNotFound)
}
