package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

func (s *Store) UpsertSchema(ctx context.Context, doc domain.CatalogSchema) error {
	now := time.Now().UTC()
	schemaJson, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("update schema: %w", err)
	}
	const q = `
		INSERT INTO catalog_schema (id, schema_json, updated_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			schema_json = excluded.schema_json,
			updated_at  = excluded.updated_at`
	_, err = s.db.ExecContext(ctx, q, string(schemaJson), now)
	if err != nil {
		return fmt.Errorf("update schema: %w", err)
	}

	return nil
}

func (s *Store) GetSchema(ctx context.Context) (domain.CatalogSchema, error) {
	var schema domain.CatalogSchema
	var schemaJson string
	err := s.db.QueryRowContext(ctx, `SELECT schema_json FROM catalog_schema WHERE id = 1`).Scan(&schemaJson)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return schema, domain.ErrSchemaNotFound
		}
		return schema, fmt.Errorf("get schema: %w", err)
	}

	err = json.Unmarshal([]byte(schemaJson), &schema)
	if err != nil {
		return schema, fmt.Errorf("get schema: %w", err)
	}
	return schema, nil
}
