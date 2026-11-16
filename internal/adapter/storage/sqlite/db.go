package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

func OpenDB(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA foreign_keys=ON;",
	}

	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite setting err: %w", err)
		}
	}

	if err = Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migration err: %w", err)
	}

	return db, nil
}
