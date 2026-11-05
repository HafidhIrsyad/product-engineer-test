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

func stockAt(t *testing.T, rows []app.StockRow, location string) app.StockRow {
	t.Helper()
	for _, r := range rows {
		if r.Location == location {
			return r
		}
	}
	t.Fatalf("no stock row for %s", location)
	return app.StockRow{}
}

// T39: one view of all stock, including what is still on the road.
func TestIntegration_StockOverview(t *testing.T) {
	f := newShipFixture(t)
	ctx := context.Background()
	_, err := f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "WH-JKT"})
	require.NoError(t, err)
	_, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: f.pallet, ToLocationCode: "DIST-MDN"})
	require.NoError(t, err)

	rows, err := f.svc.StockOverview(ctx, "", "")
	require.NoError(t, err)
	assert.Equal(t, 0, int(stockAt(t, rows, "WH-JKT").OnHand))
	onTheRoad := stockAt(t, rows, "DIST-MDN")
	assert.EqualValues(t, 480, onTheRoad.Inbound, "a destination with no stock yet still shows what is coming")
	assert.EqualValues(t, 0, onTheRoad.OnHand)

	_, err = f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "DIST-MDN"})
	require.NoError(t, err)
	rows, err = f.svc.StockOverview(ctx, "MGN-PROT-500", "DIST-MDN")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 480, rows[0].OnHand)
	assert.EqualValues(t, 480, rows[0].ATP)
	assert.EqualValues(t, 0, rows[0].Inbound)
	assert.Nil(t, rows[0].DaysOfInventory, "nothing sold yet")
}

// T40: days of inventory come from the last 14 days of sales only.
func TestIntegration_StockOverview_DaysOfInventory(t *testing.T) {
	f := newFixture(t, map[string]int{"MGN-PROT-500": 99})
	ctx := context.Background()
	for _, ago := range []string{"1 day", "20 days"} { // the second is outside the window
		_, err := f.pool.Exec(ctx, `
			INSERT INTO stock_movement (sku_id, location_id, delta_on_hand, reason, ref_type, ref_id, created_at)
			VALUES ($1, $2, -140, 'SALE_ONLINE', 'TEST', 'x', now() - $3::interval)`, f.skus["MGN-PROT-500"], f.loc, ago)
		require.NoError(t, err)
	}

	rows, err := f.svc.StockOverview(ctx, "", "WH-JKT")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 140, rows[0].Sold14d)
	require.NotNil(t, rows[0].DaysOfInventory)
	assert.InDelta(t, 9.9, *rows[0].DaysOfInventory, 0.01) // 99 units at 10 a day
	assert.True(t, rows[0].RunoutWarning)
}

// T41: units allocated to Medan and scanned in Jakarta point to their pallet and distributor.
func TestIntegration_LeakageAndAnomalies(t *testing.T) {
	f := newShipFixture(t)
	ctx := context.Background()
	_, err := f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "WH-JKT"})
	require.NoError(t, err)
	_, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: f.pallet, ToLocationCode: "DIST-MDN"})
	require.NoError(t, err)
	_, err = f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "DIST-MDN"})
	require.NoError(t, err)

	jakarta, medan := -6.2088, 3.5952
	var units []string
	rows, err := f.pool.Query(ctx, `SELECT code FROM serial WHERE level = 'UNIT' ORDER BY id LIMIT 4`)
	require.NoError(t, err)
	for rows.Next() {
		var c string
		require.NoError(t, rows.Scan(&c))
		units = append(units, c)
	}
	rows.Close()
	lng := func(v float64) *float64 { return &v }
	for i, code := range units {
		lat, lngv := jakarta, 106.8456
		if i == 3 {
			lat, lngv = medan, 98.6722 // this one is scanned at home
		}
		_, err := f.svc.Verify(ctx, app.VerifyInput{Code: domain.SerialCode(code), DeviceID: "phone-" + code, Lat: &lat, Lng: lng(lngv)})
		require.NoError(t, err)
	}

	leak, err := f.svc.Leakage(ctx)
	require.NoError(t, err)
	require.Len(t, leak, 1)
	assert.Equal(t, f.pallet, leak[0].Pallet)
	assert.Equal(t, "Distributor Medan", leak[0].Distributor)
	assert.EqualValues(t, 4, leak[0].ScannedUnits)
	assert.EqualValues(t, 3, leak[0].UnitsOutsideRegion)

	all, err := f.svc.Anomalies(ctx, app.AnomalyFilter{Since: time.Now().Add(-time.Hour)})
	require.NoError(t, err)
	assert.Len(t, all, 3)
	byType, err := f.svc.Anomalies(ctx, app.AnomalyFilter{Type: "OUT_OF_REGION", Partner: "Distributor Medan", Since: time.Now().Add(-time.Hour)})
	require.NoError(t, err)
	assert.Len(t, byType, 3)
	other, err := f.svc.Anomalies(ctx, app.AnomalyFilter{Type: "OVERSOLD", Since: time.Now().Add(-time.Hour)})
	require.NoError(t, err)
	assert.Empty(t, other)
	_, err = f.svc.Anomalies(ctx, app.AnomalyFilter{Type: "NOPE"})
	assert.ErrorIs(t, err, domain.ErrInvalidInput)

	signal, err := f.svc.SellOutSignal(ctx, time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
	require.NoError(t, err)
	require.NotEmpty(t, signal)
	var shipped, scanned int32
	for _, r := range signal {
		assert.Equal(t, "Distributor Medan", r.Partner)
		shipped += r.ShippedUnits
		scanned += r.ScannedUnits
	}
	assert.EqualValues(t, 480, shipped)
	assert.EqualValues(t, 4, scanned)
	_, err = f.svc.SellOutSignal(ctx, time.Now(), time.Now().Add(-time.Hour))
	assert.ErrorIs(t, err, domain.ErrInvalidInput)
	testdb.AssertInvariants(t, f.pool)
}
