package ingestion

import (
	"context"
	"errors"
	"testing"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

// --- Mocks ---

type mockSchemaStore struct {
	schema domain.CatalogSchema
	err    error
}

func (m *mockSchemaStore) UpsertSchema(_ context.Context, s domain.CatalogSchema) error {
	m.schema = s
	return m.err
}

func (m *mockSchemaStore) GetSchema(_ context.Context) (domain.CatalogSchema, error) {
	if m.err != nil {
		return domain.CatalogSchema{}, m.err
	}
	return m.schema, nil
}

type mockDocumentStore struct {
	called bool
	saved  []domain.Document
	err    error
}

func (m *mockDocumentStore) UpsertBatchWithOutbox(_ context.Context, docs []domain.Document) error {
	m.called = true
	m.saved = append([]domain.Document(nil), docs...)
	return m.err
}

func (m *mockDocumentStore) GetByID(_ context.Context, _ string) (*domain.Document, error) {
	return nil, nil
}

func (m *mockDocumentStore) DeleteWithOutbox(_ context.Context, _ string) error {
	return nil
}

// --- Tests ---

func TestIngestBatch_EmptyDocs(t *testing.T) {
	docStore := &mockDocumentStore{}
	schemaStore := &mockSchemaStore{err: domain.ErrSchemaNotFound}
	svc := NewService(docStore, schemaStore)

	if err := svc.IngestBatch(context.Background(), nil); err != nil {
		t.Fatalf("expected nil for empty batch, got %v", err)
	}
	if docStore.called {
		t.Fatal("expected docStore not to be called for empty batch")
	}
}

func TestIngestBatch_MissingSchema(t *testing.T) {
	docStore := &mockDocumentStore{}
	schemaStore := &mockSchemaStore{err: domain.ErrSchemaNotFound}
	svc := NewService(docStore, schemaStore)

	err := svc.IngestBatch(context.Background(), []domain.Document{
		{ID: "sku-1", Title: "Áo khoác", Price: 500000},
	})
	if !errors.Is(err, domain.ErrSchemaNotFound) {
		t.Fatalf("expected ErrSchemaNotFound, got %v", err)
	}
	if docStore.called {
		t.Fatal("expected docStore not to be called when schema is missing")
	}
}

func TestIngestBatch_DomainAgnostic(t *testing.T) {
	ctx := context.Background()

	t.Run("fashion shop", func(t *testing.T) {
		docStore := &mockDocumentStore{}
		schemaStore := &mockSchemaStore{
			schema: domain.CatalogSchema{
				ShopName: "Saigon Streetwear",
				Currency: "VND",
				Facets: []domain.FacetDef{
					{Key: "size", Type: domain.FacetTypeEnum, Values: []string{"S", "M", "L"}, Filterable: true},
					{Key: "waterproof", Type: domain.FacetTypeBool, Filterable: true},
				},
			},
		}
		svc := NewService(docStore, schemaStore)

		err := svc.IngestBatch(ctx, []domain.Document{
			{
				ID:          " jacket-1 ",
				Title:       " Áo khoác gió ",
				Price:       850000,
				Attributes:  map[string]any{"size": []string{"M", "L"}, "waterproof": true},
				RAGContents: []string{"Giữ ấm tốt khi đi Đà Lạt"},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !docStore.called || len(docStore.saved) != 1 {
			t.Fatalf("expected 1 saved doc, got called=%v len=%d", docStore.called, len(docStore.saved))
		}
		got := docStore.saved[0]
		if got.ID != "jacket-1" || got.Currency != "VND" || got.ContentHash == "" || got.SyncStatus != domain.SyncStatusPending {
			t.Fatalf("unexpected prepared doc: %+v", got)
		}
	})

	t.Run("laptop shop", func(t *testing.T) {
		docStore := &mockDocumentStore{}
		schemaStore := &mockSchemaStore{
			schema: domain.CatalogSchema{
				ShopName: "TechGear VN",
				Currency: "VND",
				Facets: []domain.FacetDef{
					{Key: "brand", Type: domain.FacetTypeEnum, Values: []string{"Apple", "Dell"}, Filterable: true},
					{Key: "ram_gb", Type: domain.FacetTypeNumber, Filterable: true},
				},
			},
		}
		svc := NewService(docStore, schemaStore)

		err := svc.IngestBatch(ctx, []domain.Document{
			{
				ID:         "lap-1",
				Title:      "Dell XPS 13",
				Price:      25000000,
				Attributes: map[string]any{"brand": "Dell", "ram_gb": float64(16)},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !docStore.called {
			t.Fatal("expected docStore to be called")
		}
	})
}

func TestIngestBatch_ValidationErrors(t *testing.T) {
	schemaStore := &mockSchemaStore{
		schema: domain.CatalogSchema{
			ShopName: "TechGear VN",
			Currency: "VND",
			Facets: []domain.FacetDef{
				{Key: "brand", Type: domain.FacetTypeEnum, Values: []string{"Apple", "Dell"}},
				{Key: "ram_gb", Type: domain.FacetTypeNumber},
				{Key: "touchscreen", Type: domain.FacetTypeBool},
			},
		},
	}

	cases := []struct {
		name string
		docs []domain.Document
	}{
		{
			name: "unknown attribute",
			docs: []domain.Document{{ID: "1", Title: "Laptop", Attributes: map[string]any{"cpu": "M3"}}},
		},
		{
			name: "invalid enum value",
			docs: []domain.Document{{ID: "1", Title: "Laptop", Attributes: map[string]any{"brand": "Acer"}}},
		},
		{
			name: "wrong number type",
			docs: []domain.Document{{ID: "1", Title: "Laptop", Attributes: map[string]any{"ram_gb": "16GB"}}},
		},
		{
			name: "wrong bool type",
			docs: []domain.Document{{ID: "1", Title: "Laptop", Attributes: map[string]any{"touchscreen": "yes"}}},
		},
		{
			name: "duplicate id in batch",
			docs: []domain.Document{
				{ID: "dup-1", Title: "Laptop A"},
				{ID: "dup-1", Title: "Laptop B"},
			},
		},
		{
			name: "negative price",
			docs: []domain.Document{{ID: "1", Title: "Laptop", Price: -100}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docStore := &mockDocumentStore{}
			svc := NewService(docStore, schemaStore)

			err := svc.IngestBatch(context.Background(), tc.docs)
			if !errors.Is(err, domain.ErrInvalidDocument) {
				t.Fatalf("expected ErrInvalidDocument, got %v", err)
			}
			if docStore.called {
				t.Fatal("expected docStore not to be called on validation error")
			}
		})
	}
}
