package schema

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tienlbhenry/siftcart/internal/domain"
	"github.com/tienlbhenry/siftcart/internal/ports"
)

type Service struct {
	store ports.SchemaStore
}

func NewService(store ports.SchemaStore) *Service {
	return &Service{
		store: store,
	}
}

func (s *Service) SetSchema(ctx context.Context, schema domain.CatalogSchema) error {

	schema.ShopName = strings.TrimSpace(schema.ShopName)
	schema.WebhookURL = strings.TrimSpace(schema.WebhookURL)
	schema.Currency = strings.ToUpper(strings.TrimSpace(schema.Currency))

	if schema.Facets == nil {
		schema.Facets = []domain.FacetDef{}
	}

	if err := schema.Validate(); err != nil {
		return fmt.Errorf("invalid schema: %w", err)
	}

	schema.UpdatedAt = time.Now().UTC()
	err := s.store.UpsertSchema(ctx, schema)
	if err != nil {
		return fmt.Errorf("could not upsert schema: %w", err)
	}

	return nil
}

func (s *Service) GetSchema(ctx context.Context) (domain.CatalogSchema, error) {
	return s.store.GetSchema(ctx)
}
