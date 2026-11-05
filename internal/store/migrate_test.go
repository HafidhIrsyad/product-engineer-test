package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HafidhIrsyad/product-engineer-test/internal/store"
	"github.com/HafidhIrsyad/product-engineer-test/internal/testdb"
)

// T20: the schema under test is exactly the schema the API runs, and migrating twice is safe.
func TestIntegration_Migrate_Idempotent(t *testing.T) {
	pool := testdb.Pool(t) // first Migrate already ran when the container started
	ctx := context.Background()

	require.NoError(t, store.Migrate(ctx, pool.Config().ConnConfig.ConnString()), "second Migrate must be a no-op")

	files, err := filepath.Glob("migrations/*.sql")
	require.NoError(t, err)
	var version int
	require.NoError(t, pool.QueryRow(ctx, `SELECT MAX(version_id) FROM goose_db_version`).Scan(&version))
	require.Equal(t, len(files), version, "goose version must equal the number of migration files")

	for _, table := range []string{"serial", "stock_balance", "orders", "pos_transaction", "pos_device", "anomaly"} {
		var exists bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists))
		require.True(t, exists, "table %s must exist", table)
	}

	testdb.AssertInvariants(t, pool) // also proves the invariant queries are valid SQL
}
