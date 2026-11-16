package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/tienlbhenry/siftcart/migrations"
)

func Migrate(ctx context.Context, db *sql.DB) error {
	const createMigrationsTable = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at DATETIME NOT NULL
		);
	`

	if _, err := db.ExecContext(ctx, createMigrationsTable); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrations.SQLiteFS, "sqlite")
	if err != nil {
		return fmt.Errorf("read migrations directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		if err := applyMigration(ctx, db, entry.Name()); err != nil {
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
	}

	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, filename string) error {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = ?", filename).
		Scan(&count)
	if err != nil {
		return fmt.Errorf("check if migration %s already applied: %w", filename, err)
	}

	if count > 0 {
		return nil // skip already applied migration
	}

	sqlBytes, err := fs.ReadFile(migrations.SQLiteFS, "sqlite/"+filename)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", filename, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, string(sqlBytes), time.Now()); err != nil {
		return fmt.Errorf("apply migration %s: %w", filename, err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		filename, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("insert migration %s into schema_migrations: %w", filename, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
