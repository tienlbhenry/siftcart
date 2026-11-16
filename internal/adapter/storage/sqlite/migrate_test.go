package sqlite

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrate_Idempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()
	ctx := context.Background()

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("first migration failed: %v", err)
	}

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second migration failed: %v", err)
	}

}
