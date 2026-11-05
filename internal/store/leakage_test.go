package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

// pallet builds one pallet with one carton and n units allocated to a distributor, every unit
// scanned once, and the first `outside` of them flagged OUT_OF_REGION. It returns the pallet code.
func pallet(t *testing.T, p *pgxpool.Pool, sku, loc, partner int64, n, outside int) string {
	t.Helper()
	ctx := context.Background()
	palletID, palletCode := testdb.InsertSerial(t, p, testdb.SerialSpec{SKUID: sku, LocationID: loc, Level: "PALLET"})
	cartonID, _ := testdb.InsertSerial(t, p, testdb.SerialSpec{SKUID: sku, LocationID: loc, Level: "CARTON", ParentID: &palletID})
	for i := range n {
		unitID, code := testdb.InsertSerial(t, p, testdb.SerialSpec{SKUID: sku, LocationID: loc, Status: "IN_STOCK", ParentID: &cartonID, PartnerID: &partner})
		_, err := p.Exec(ctx, `INSERT INTO scan_event (serial_id, raw_code, device_id, verdict) VALUES ($1, $2, 'd1', 'GENUINE')`, unitID, code)
		require.NoError(t, err)
		if i < outside {
			_, err = p.Exec(ctx, `INSERT INTO anomaly (type, serial_id, partner_id) VALUES ('OUT_OF_REGION', $1, $2)`, unitID, partner)
			require.NoError(t, err)
		}
	}
	return palletCode
}

// T23: the pallet query uses a relative threshold: at least 3 units AND at least 30% of the
// units scanned from that pallet.
func TestIntegration_LeakageByPallet(t *testing.T) {
	p := testdb.Pool(t)
	st := testdb.Store(t, p)
	sku := testdb.InsertSKU(t, p, "MGN-PROT-500")
	loc := testdb.InsertWarehouse(t, p, "WH-JKT")
	var partner int64
	require.NoError(t, p.QueryRow(context.Background(), `
		INSERT INTO partner (name, region_name, region_lat, region_lng, region_radius_km)
		VALUES ('Distributor Medan', 'Sumatera Utara', 3.5952, 98.6722, 150) RETURNING id`).Scan(&partner))

	pallet(t, p, sku, loc, partner, 10, 2)            // 2 units outside: below 3
	leaking := pallet(t, p, sku, loc, partner, 10, 4) // 4 of 10 scanned: 40%
	pallet(t, p, sku, loc, partner, 20, 4)            // 4 of 20 scanned: only 20%
	pallet(t, p, sku, loc, partner, 10, 0)            // nothing outside

	rows, err := st.LeakageByPallet(context.Background(), store.LeakageByPalletParams{MinUnits: 3, MinRatio: 0.3})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, leaking, rows[0].Pallet)
	require.Equal(t, "Distributor Medan", rows[0].Distributor)
	require.EqualValues(t, 10, rows[0].ScannedUnits)
	require.EqualValues(t, 4, rows[0].UnitsOutsideRegion)
}
