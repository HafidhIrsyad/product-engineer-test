package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/domain"
	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

// Internal test (package app): transitionSerial is not part of the public API.
// It is called through InTx, exactly like the use cases do.
func move(t *testing.T, st *store.Store, id int64, from, to domain.SerialStatus) error {
	t.Helper()
	return st.InTx(context.Background(), func(q *store.Queries) error {
		_, err := transitionSerial(context.Background(), q, id, from, to, nil, "TEST", "t4")
		return err
	})
}

func statusOf(t *testing.T, st *store.Store, id int64) domain.SerialStatus {
	t.Helper()
	row, err := st.GetSerialStatus(context.Background(), id)
	require.NoError(t, err)
	return row
}

// T4: the whole lifecycle of one serial through the database, plus a stale read.
func TestIntegration_SerialLifecycle(t *testing.T) {
	pool := testdb.Pool(t)
	st := testdb.Store(t, pool)
	sku := testdb.InsertSKU(t, pool, "MGN-PROT-500")
	loc := testdb.InsertWarehouse(t, pool, "WH-JKT")
	id, _ := testdb.InsertSerial(t, pool, testdb.SerialSpec{SKUID: sku, LocationID: loc, Status: "PRODUCED"})

	path := []domain.SerialStatus{
		domain.SerialProduced, domain.SerialInWarehouse, domain.SerialInTransit,
		domain.SerialInStock, domain.SerialSold, domain.SerialVerified,
	}
	for i := 1; i < len(path); i++ {
		require.NoError(t, move(t, st, id, path[i-1], path[i]), "%s → %s", path[i-1], path[i])
	}
	assert.Equal(t, domain.SerialVerified, statusOf(t, st, id))

	rows, err := pool.Query(context.Background(), `SELECT from_status, to_status FROM serial_event WHERE serial_id = $1 ORDER BY id`, id)
	require.NoError(t, err)
	defer rows.Close()
	var got [][2]string
	for rows.Next() {
		var from, to string
		require.NoError(t, rows.Scan(&from, &to))
		got = append(got, [2]string{from, to})
	}
	want := [][2]string{
		{"PRODUCED", "IN_WAREHOUSE"}, {"IN_WAREHOUSE", "IN_TRANSIT"}, {"IN_TRANSIT", "IN_STOCK"},
		{"IN_STOCK", "SOLD"}, {"SOLD", "VERIFIED"},
	}
	assert.Equal(t, want, got, "the history is complete and in order")

	var stamped bool
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT first_verified_at IS NOT NULL FROM serial WHERE id = $1`, id).Scan(&stamped))
	assert.True(t, stamped, "the first move to VERIFIED stamps first_verified_at")

	t.Run("a stale read is rejected by the database", func(t *testing.T) {
		// The caller still believes the serial is IN_WAREHOUSE (a legal move to IN_TRANSIT), but it is VERIFIED now.
		err := move(t, st, id, domain.SerialInWarehouse, domain.SerialInTransit)
		assert.ErrorIs(t, err, domain.ErrStaleStatus)
		assert.Equal(t, domain.SerialVerified, statusOf(t, st, id), "nothing changed")
	})

	t.Run("a move outside the lifecycle is rejected before touching the database", func(t *testing.T) {
		other, _ := testdb.InsertSerial(t, pool, testdb.SerialSpec{SKUID: sku, LocationID: loc, Status: "PRODUCED"})
		assert.ErrorIs(t, move(t, st, other, domain.SerialProduced, domain.SerialSold), domain.ErrInvalidTransition)
		assert.ErrorIs(t, move(t, st, id, domain.SerialVerified, domain.SerialInStock), domain.ErrInvalidTransition, "VERIFIED is final")
		assert.Equal(t, domain.SerialProduced, statusOf(t, st, other))
	})
}

// Two processes try the same move at the same time: exactly one wins, the other sees a stale status.
func TestIntegration_SerialTransition_Race(t *testing.T) {
	pool := testdb.Pool(t)
	st := testdb.Store(t, pool)
	sku := testdb.InsertSKU(t, pool, "MGN-PROT-500")
	loc := testdb.InsertWarehouse(t, pool, "WH-JKT")
	id, _ := testdb.InsertSerial(t, pool, testdb.SerialSpec{SKUID: sku, LocationID: loc, Status: "IN_STOCK"})

	var won, stale atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := move(t, st, id, domain.SerialInStock, domain.SerialSold); err == nil {
				won.Add(1)
			} else {
				assert.ErrorIs(t, err, domain.ErrStaleStatus)
				stale.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.EqualValues(t, 1, won.Load())
	assert.EqualValues(t, 7, stale.Load())
	var events int
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM serial_event WHERE serial_id = $1`, id).Scan(&events))
	assert.Equal(t, 1, events, "only the winner wrote history")
}
