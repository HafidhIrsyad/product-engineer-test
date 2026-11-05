package app_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/app"
	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

// atPartner prints a pallet with labels, ships it to the distributor and returns the unit labels.
func atPartner(t *testing.T) (shipFixture, []app.UnitLabel) {
	t.Helper()
	f := newShipFixture(t)
	ctx := context.Background()
	in := batchInput(1)
	in.BatchCode = "B-LABELS"
	batch, err := f.svc.CreateBatch(ctx, in)
	require.NoError(t, err)
	require.Len(t, batch.Labels, 480+0)
	for _, step := range []func() error{
		func() error {
			_, err := f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: batch.Pallets[0].Code, LocationCode: "WH-JKT"})
			return err
		},
		func() error {
			_, err := f.svc.Ship(ctx, app.ShipInput{ContainerCode: batch.Pallets[0].Code, ToLocationCode: "DIST-MDN"})
			return err
		},
		func() error {
			_, err := f.svc.Receive(ctx, app.ReceiveInput{ContainerCode: batch.Pallets[0].Code, LocationCode: "DIST-MDN"})
			return err
		},
	} {
		require.NoError(t, step())
	}
	return f, batch.Labels
}

// T44: the inner code under the seal proves the purchase; the claim books the unit out of the partner's stock.
func TestIntegration_ClaimPoints(t *testing.T) {
	f, labels := atPartner(t)
	ctx := context.Background()

	view, err := f.svc.ClaimPoints(ctx, app.ClaimInput{MemberID: 7, SerialCode: labels[0].SerialCode, InnerCode: labels[0].InnerCode})
	require.NoError(t, err)
	assert.Equal(t, "PENDING", view.Status)
	assert.EqualValues(t, 10, view.Points)

	assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM serial WHERE code = $1 AND status = 'VERIFIED' AND first_verified_at IS NOT NULL`, labels[0].SerialCode))
	assert.Equal(t, 479, f.onHand(t, "DIST-MDN"))
	assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM stock_movement WHERE reason = 'SELL_OUT_INFERRED' AND delta_on_hand = -1`))
	testdb.AssertInvariants(t, f.pool)

	points, err := f.svc.Points(ctx, 7)
	require.NoError(t, err)
	assert.EqualValues(t, 10, points.PendingPoints)
	assert.EqualValues(t, 0, points.AvailablePoints, "points wait out the review window")

	_, err = f.pool.Exec(ctx, `UPDATE point_claim SET available_at = now() - interval '1 minute'`)
	require.NoError(t, err)
	points, err = f.svc.Points(ctx, 7)
	require.NoError(t, err)
	assert.EqualValues(t, 10, points.AvailablePoints)
	assert.EqualValues(t, 0, points.PendingPoints)

	t.Run("scanning the outer QR never creates points", func(t *testing.T) {
		got, err := f.svc.Verify(ctx, app.VerifyInput{Code: domain.SerialCode(labels[1].SerialCode), DeviceID: "shelf-phone"})
		require.NoError(t, err)
		assert.Equal(t, domain.VerdictGenuine, got.Verdict)
		assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM point_claim`))
		assert.Equal(t, 479, f.onHand(t, "DIST-MDN"))
	})

	t.Run("the secret is stored as a hash only", func(t *testing.T) {
		assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM serial WHERE inner_code_hash = convert_to($1, 'UTF8')`, labels[1].InnerCode))
		assert.Equal(t, 960, f.count(t, `SELECT COUNT(*) FROM serial WHERE level = 'UNIT' AND length(inner_code_hash) = 32`), "every unit of both batches")
		assert.Equal(t, 0, f.count(t, `SELECT COUNT(*) FROM serial WHERE level <> 'UNIT' AND inner_code_hash IS NOT NULL`))
	})
}

// T45: every refusal leaves the unit and the stock untouched.
func TestIntegration_ClaimPoints_Refused(t *testing.T) {
	f, labels := atPartner(t)
	ctx := context.Background()
	claim := func(code, inner string) error {
		_, err := f.svc.ClaimPoints(ctx, app.ClaimInput{MemberID: 1, SerialCode: code, InnerCode: inner})
		return err
	}

	assert.ErrorIs(t, claim(labels[0].SerialCode, "WRONG123"), domain.ErrProofRejected)
	assert.ErrorIs(t, claim(labels[0].SerialCode, labels[1].InnerCode), domain.ErrProofRejected, "the inner code of another unit")
	assert.ErrorIs(t, claim(string(domain.NewSerialCode()), labels[0].InnerCode), domain.ErrProofRejected, "an unknown unit looks the same as a wrong code")
	assert.Equal(t, 480, f.onHand(t, "DIST-MDN"))

	require.NoError(t, claim(labels[0].SerialCode, labels[0].InnerCode))
	assert.ErrorIs(t, claim(labels[0].SerialCode, labels[0].InnerCode), domain.ErrAlreadyClaimed)

	var pallet string
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT code FROM serial WHERE level = 'PALLET'`).Scan(&pallet))
	assert.ErrorIs(t, claim(pallet, "ANYTHING"), domain.ErrProofRejected, "a pallet has no inner code")

	_, err := f.pool.Exec(ctx, `UPDATE serial SET status = 'QUARANTINE' WHERE code = $1`, labels[2].SerialCode)
	require.NoError(t, err)
	assert.ErrorIs(t, claim(labels[2].SerialCode, labels[2].InnerCode), domain.ErrProofRejected, "only stock sitting at the partner can be claimed")

	var ownUnit string // sold and scanned at an own store: verified, but nobody claimed it
	require.NoError(t, f.pool.QueryRow(ctx, `SELECT code FROM serial WHERE level = 'UNIT' AND location_id = (SELECT id FROM location WHERE code = 'FACTORY-01') ORDER BY id LIMIT 1`).Scan(&ownUnit))
	_, err = f.pool.Exec(ctx, `UPDATE serial SET status = 'VERIFIED' WHERE code = $1`, ownUnit)
	require.NoError(t, err)
	assert.ErrorIs(t, claim(ownUnit, "ABCDEFGHJKMN"), domain.ErrProofRejected, "outside a partner a unit is never 'already claimed'")
}

// T46: ten members claim the same unit at the same moment: one gets the points. And a member who
// claims faster than a person can is held for review.
func TestIntegration_ClaimPoints_Race(t *testing.T) {
	f, labels := atPartner(t)
	ctx := context.Background()

	var mu sync.Mutex
	won, already := 0, 0
	runTogether(10, func(i int) {
		_, err := f.svc.ClaimPoints(ctx, app.ClaimInput{MemberID: int64(100 + i), SerialCode: labels[0].SerialCode, InnerCode: labels[0].InnerCode})
		mu.Lock()
		defer mu.Unlock()
		switch {
		case err == nil:
			won++
		case errorIsOneOf(err, domain.ErrAlreadyClaimed):
			already++
		default:
			assert.NoError(t, err)
		}
	})
	assert.Equal(t, 1, won)
	assert.Equal(t, 9, already)
	assert.Equal(t, 1, f.count(t, `SELECT COUNT(*) FROM point_claim`))
	assert.Equal(t, 479, f.onHand(t, "DIST-MDN"), "stock left once")

	var statuses []string
	for i := 1; i <= domain.MaxClaimsPerDay+2; i++ {
		view, err := f.svc.ClaimPoints(ctx, app.ClaimInput{MemberID: 999, SerialCode: labels[i].SerialCode, InnerCode: labels[i].InnerCode})
		require.NoError(t, err, fmt.Sprint(i))
		statuses = append(statuses, view.Status)
	}
	assert.Equal(t, "PENDING", statuses[domain.MaxClaimsPerDay-1])
	assert.Equal(t, "HELD", statuses[domain.MaxClaimsPerDay], "the 11th claim within a day is held for review")
	assert.Equal(t, "HELD", statuses[domain.MaxClaimsPerDay+1], "and so is every later one")
	testdb.AssertInvariants(t, f.pool)
}
