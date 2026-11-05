package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

// shipFixture has a factory, the warehouse WH-JKT, a distributor warehouse DIST-MDN and one printed pallet.
type shipFixture struct {
	fixture
	pallet string
	distID int64
}

func newShipFixture(t *testing.T) shipFixture {
	t.Helper()
	f := newFixture(t, map[string]int{"MGN-PROT-500": 0})
	testdb.InsertFactory(t, f.pool, "FACTORY-01")
	ctx := context.Background()
	var partnerID int64
	require.NoError(t, f.pool.QueryRow(ctx, `
		INSERT INTO partner (name, region_name, region_lat, region_lng, region_radius_km)
		VALUES ('Distributor Medan', 'Sumatera Utara', 3.5952, 98.6722, 150) RETURNING id`).Scan(&partnerID))
	var distID int64
	require.NoError(t, f.pool.QueryRow(ctx, `
		INSERT INTO location (code, name, type, partner_id, lat, lng)
		VALUES ('DIST-MDN', 'Gudang Distributor Medan', 'PARTNER', $1, 3.5952, 98.6722) RETURNING id`, partnerID).Scan(&distID))
	batch, err := f.svc.CreateBatch(ctx, batchInput(1))
	require.NoError(t, err)
	return shipFixture{fixture: f, pallet: batch.Pallets[0].Code, distID: distID}
}

func (f shipFixture) onHand(t *testing.T, location string) int {
	t.Helper()
	return f.count(t, `SELECT COALESCE(SUM(on_hand), 0) FROM stock_balance b JOIN location l ON l.id = b.location_id WHERE l.code = $1`, location)
}

// T27: factory to warehouse to distributor. Status, location, partner, stock and history all follow.
func TestIntegration_Shipment_FullJourney(t *testing.T) {
	f := newShipFixture(t)
	ctx := context.Background()

	view, err := f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "WH-JKT"})
	require.NoError(t, err)
	assert.Equal(t, domain.SerialInWarehouse, view.Status)
	assert.Equal(t, 480, view.Units)
	assert.Equal(t, 480, f.onHand(t, "WH-JKT"), "the pallet's units become stock")
	assert.Equal(t, 521, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'IN_WAREHOUSE'`), "pallet, cartons and units all moved")
	testdb.AssertInvariants(t, f.pool)

	view, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: f.pallet, ToLocationCode: "DIST-MDN"})
	require.NoError(t, err)
	assert.Equal(t, domain.SerialInTransit, view.Status)
	assert.Equal(t, 0, f.onHand(t, "WH-JKT"))
	assert.Equal(t, 521, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'IN_TRANSIT' AND partner_id IS NOT NULL`), "shipping to a partner ties every unit to the distributor")
	testdb.AssertInvariants(t, f.pool)

	view, err = f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "DIST-MDN"})
	require.NoError(t, err)
	assert.Equal(t, domain.SerialInStock, view.Status)
	assert.Equal(t, 480, f.onHand(t, "DIST-MDN"))
	assert.Equal(t, 521, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'IN_STOCK' AND location_id = $1`, f.distID))
	assert.Equal(t, 4*521, f.count(t, `SELECT COUNT(*) FROM serial_event`), "produced, received, shipped, received: four events per serial")
	assert.Equal(t, 3, f.count(t, `SELECT COUNT(*) FROM stock_movement WHERE reason IN ('RECEIVE', 'SHIP_OUT') AND abs(delta_on_hand) = 480`), "ledger: two receives and one shipment")
	testdb.AssertInvariants(t, f.pool)

	trace, err := f.svc.TraceSerial(ctx, domain.SerialCode(f.pallet))
	require.NoError(t, err)
	require.NotNil(t, trace.Partner)
	assert.Equal(t, "Distributor Medan", *trace.Partner)
	assert.Len(t, trace.History, 4)
}

// T28: stock promised to an order cannot be shipped away, and nothing changes when it is refused.
func TestIntegration_Shipment_CannotShipReservedStock(t *testing.T) {
	f := newShipFixture(t)
	ctx := context.Background()
	_, err := f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "WH-JKT"})
	require.NoError(t, err)
	_, _, err = f.svc.CreateOrder(ctx, input(uuid.New(), app.OrderItem{SKUCode: "MGN-PROT-500", Qty: 1}))
	require.NoError(t, err)

	_, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: f.pallet, ToLocationCode: "DIST-MDN"})
	assert.ErrorIs(t, err, domain.ErrInsufficientStock)
	assert.Equal(t, 480, f.onHand(t, "WH-JKT"))
	assert.Equal(t, 521, f.count(t, `SELECT COUNT(*) FROM serial WHERE status = 'IN_WAREHOUSE'`), "the refused shipment moved nothing")
	testdb.AssertInvariants(t, f.pool)
}

// T29: the same container shipped by many callers at once: one wins, nobody deadlocks.
func TestIntegration_Shipment_Race(t *testing.T) {
	f := newShipFixture(t)
	ctx := context.Background()
	_, err := f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "WH-JKT"})
	require.NoError(t, err)
	var carton string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT code FROM serial WHERE level = 'CARTON' ORDER BY id LIMIT 1`).Scan(&carton))

	// Six callers ship the pallet, two ship one of its cartons: they all want the same rows.
	codes := []string{f.pallet, f.pallet, f.pallet, f.pallet, f.pallet, f.pallet, carton, carton}
	errs := make([]error, len(codes))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, code := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = f.svc.Ship(ctx, app.ShipInput{ContainerCode: code, ToLocationCode: "DIST-MDN"})
		}()
	}
	close(start)
	wg.Wait()

	won := 0
	for _, err := range errs {
		if err == nil {
			won++
			continue
		}
		assert.True(t, errorIsOneOf(err, domain.ErrInvalidTransition, domain.ErrStaleStatus, domain.ErrMixedContainer, domain.ErrInvalidInput), "unexpected error: %v", err)
	}
	assert.Equal(t, 1, won, "exactly one shipment wins")
	testdb.AssertInvariants(t, f.pool)
}

// T30: refused moves leave everything as it was.
func TestIntegration_Shipment_Refused(t *testing.T) {
	f := newShipFixture(t)
	ctx := context.Background()
	testdb.InsertStore(t, f.pool, "HUB-BDG", 0)

	_, err := f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "HUB-BDG"})
	assert.ErrorIs(t, err, domain.ErrInvalidInput, "new production is received at a central warehouse")
	_, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: f.pallet, ToLocationCode: "DIST-MDN"})
	assert.ErrorIs(t, err, domain.ErrInvalidTransition, "a PRODUCED container is not in a warehouse yet")
	_, err = f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "WH-JKT"})
	require.NoError(t, err)

	_, err = f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "WH-JKT"})
	assert.ErrorIs(t, err, domain.ErrInvalidTransition, "already received")
	_, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: f.pallet, ToLocationCode: "WH-JKT"})
	assert.ErrorIs(t, err, domain.ErrInvalidInput, "same location")
	_, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: f.pallet, ToLocationCode: "NOPE"})
	assert.ErrorIs(t, err, domain.ErrUnknownLocation)
	_, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: string(domain.NewSerialCode()), ToLocationCode: "DIST-MDN"})
	assert.ErrorIs(t, err, domain.ErrSerialNotFound)

	_, err = f.svc.Ship(ctx, app.ShipInput{ContainerCode: f.pallet, ToLocationCode: "DIST-MDN"})
	require.NoError(t, err)
	_, err = f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: f.pallet, LocationCode: "HUB-BDG"})
	assert.ErrorIs(t, err, domain.ErrWrongLocation)

	t.Run("a pallet with one unit set apart cannot travel as a whole", func(t *testing.T) {
		g := newShipFixture(t)
		_, err := g.svc.Receive(ctx, app.ReceiveInput{ContainerCode: g.pallet, LocationCode: "WH-JKT"})
		require.NoError(t, err)
		_, err = g.pool.Exec(ctx, `UPDATE serial SET status = 'QUARANTINE' WHERE id = (SELECT MIN(id) FROM serial WHERE level = 'UNIT')`)
		require.NoError(t, err)
		_, err = g.svc.Ship(ctx, app.ShipInput{ContainerCode: g.pallet, ToLocationCode: "DIST-MDN"})
		assert.ErrorIs(t, err, domain.ErrMixedContainer)
	})
}

func errorIsOneOf(err error, targets ...error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
