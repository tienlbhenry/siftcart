package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) UpsertBatchWithOutbox(ctx context.Context, docs []domain.Document) error {
	if len(docs) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	for _, doc := range docs {
		var nextVersion int64
		var payloadChanged int
		var oldHash string
		var oldVersion int64
		err = tx.QueryRowContext(ctx, `SELECT content_hash, version FROM documents WHERE id = ?`, doc.ID).
			Scan(&oldHash, &oldVersion)

		switch {
		case errors.Is(err, sql.ErrNoRows): // new document, skip
			nextVersion = 1
			payloadChanged = 1
		case err != nil:
			return fmt.Errorf("failed to query document ID %s: %w", doc.ID, err)
		default:
			nextVersion = oldVersion + 1
			if oldHash != doc.ContentHash {
				payloadChanged = 1
			}
		}

		doc.Version = nextVersion
		doc.SyncStatus = domain.SyncStatusPending
		doc.UpdatedAt = now

		if doc.Categories == nil {
			doc.Categories = []string{}
		}
		if doc.Attributes == nil {
			doc.Attributes = map[string]any{}
		}
		if doc.RAGContents == nil {
			doc.RAGContents = []string{}
		}

		var categoriesJson, attributesJson, ragContentsJson []byte
		categoriesJson, err = json.Marshal(doc.Categories)
		if err != nil {
			return fmt.Errorf("failed to marshal document ID %s categories: %w", doc.ID, err)
		}
		attributesJson, err = json.Marshal(doc.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal document ID %s attributes: %w", doc.ID, err)
		}
		ragContentsJson, err = json.Marshal(doc.RAGContents)
		if err != nil {
			return fmt.Errorf("failed to marshal document ID %s rag contents: %w", doc.ID, err)
		}

		const q = `INSERT INTO documents (id, title, price, currency, in_stock, categories_json, 
                       attributes_json, rag_contents_json, content_hash, version, 
                       sync_status, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (id) 
					DO UPDATE SET title = excluded.title, price = excluded.price, currency = excluded.currency,
					    in_stock = excluded.in_stock, categories_json = excluded.categories_json, 
					    attributes_json = excluded.attributes_json, rag_contents_json = excluded.rag_contents_json, 
					    content_hash = excluded.content_hash, version = excluded.version, 
					    sync_status = excluded.sync_status, updated_at = excluded.updated_at`

		_, err = tx.ExecContext(ctx, q,
			doc.ID, doc.Title, doc.Price, doc.Currency, boolToInt(doc.InStock),
			string(categoriesJson), string(attributesJson), string(ragContentsJson),
			doc.ContentHash, doc.Version, string(doc.SyncStatus), doc.UpdatedAt)
		if err != nil {
			return fmt.Errorf("upsert doc %q: %w", doc.ID, err)
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO outbox_events (doc_id, action, version, payload_changed, status, created_at) 
					VALUES (?, ?, ?, ?, ?, ?)`,
			doc.ID, domain.OutboxEventActionUpsert, doc.Version, payloadChanged, domain.OutboxEventStatusPending, now)
		if err != nil {
			return fmt.Errorf("insert outbox for doc %q: %w", doc.ID, err)
		}
	}

	err = tx.Commit()
	if err != nil {
		return err
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) GetByID(ctx context.Context, id string) (*domain.Document, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: empty document ID", domain.ErrInvalidDocument)
	}
	const q = `
		SELECT id, title, price, currency, in_stock,
		       categories_json, attributes_json, rag_contents_json,
		       content_hash, version, sync_status, updated_at
		FROM documents
		WHERE id = ?`
	var (
		doc            domain.Document
		inStockInt     int
		categoriesJSON string
		attributesJSON string
		ragJSON        string
		syncStatus     string
	)
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&doc.ID, &doc.Title, &doc.Price, &doc.Currency, &inStockInt,
		&categoriesJSON, &attributesJSON, &ragJSON,
		&doc.ContentHash, &doc.Version, &syncStatus, &doc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: doc %q", domain.ErrDocumentNotFound, id)
		}
		return nil, fmt.Errorf("query document %q: %w", id, err)
	}
	doc.InStock = inStockInt == 1
	doc.SyncStatus = domain.SyncStatusType(syncStatus)
	if err := json.Unmarshal([]byte(categoriesJSON), &doc.Categories); err != nil {
		return nil, fmt.Errorf("unmarshal categories for %q: %w", id, err)
	}
	if err := json.Unmarshal([]byte(attributesJSON), &doc.Attributes); err != nil {
		return nil, fmt.Errorf("unmarshal attributes for %q: %w", id, err)
	}
	if err := json.Unmarshal([]byte(ragJSON), &doc.RAGContents); err != nil {
		return nil, fmt.Errorf("unmarshal rag_contents for %q: %w", id, err)
	}
	return &doc, nil
}

func (s *Store) DeleteWithOutbox(ctx context.Context, id string) error {
	if len(id) == 0 {
		return fmt.Errorf("empty document ID %w", domain.ErrInvalidDocument)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer tx.Rollback()

	var nextVersion int64
	var oldVersion int64
	now := time.Now().UTC()
	err = tx.QueryRowContext(ctx, `SELECT version FROM documents WHERE id = ?`, id).
		Scan(&oldVersion)

	switch {
	case errors.Is(err, sql.ErrNoRows): //
		return fmt.Errorf("%w: doc %q", domain.ErrDocumentNotFound, id)
	case err != nil:
		return fmt.Errorf("failed to query document ID %s: %w", id, err)
	default:
		nextVersion = oldVersion + 1
	}

	_, err = tx.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete document %q: %w", id, err)
	}

	const q = `INSERT INTO outbox_events (doc_id, action, version, payload_changed, status, created_at) 
					VALUES (?, ?, ?, ?, ?, ?)`

	_, err = tx.ExecContext(ctx, q, id, domain.OutboxEventActionDelete, nextVersion, 0, domain.OutboxEventStatusPending, now)
	if err != nil {
		return fmt.Errorf("insert outbox for doc %q: %w", id, err)
	}

	err = tx.Commit()
	if err != nil {
		return err
	}
	return nil
}
