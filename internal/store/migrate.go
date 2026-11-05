package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver for database/sql, used by goose
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies every pending migration. goose records versions in goose_db_version,
// so calling it again is a no-op. The API (at start) and testdb (before integration tests)
// both call this, so tests run against exactly the production schema (ARCHITECTURE.md §5.1).
func Migrate(ctx context.Context, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open db for migration: %w", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	return goose.UpContext(ctx, db, "migrations")
}
