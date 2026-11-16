package ports

import (
	"context"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

type SchemaStore interface {
	UpsertSchema(context.Context, domain.CatalogSchema) error
	GetSchema(context.Context) (domain.CatalogSchema, error)
}

type DocumentStore interface {
	UpsertBatchWithOutbox(context.Context, []domain.Document) error
	GetByID(context.Context, string) (*domain.Document, error)
	DeleteWithOutbox(context.Context, string) error
}

type OutboxStore interface {
	FetchPending(ctx context.Context, limit int) ([]domain.OutboxEvent, error)
	MarkDone(ctx context.Context, eventID int64) error
	MarkFailed(ctx context.Context, eventID int64, errMsg string) error
}
