package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/tienlbhenry/siftcart/internal/domain"
	"github.com/tienlbhenry/siftcart/internal/ports"
)

type Service struct {
	documentStore ports.DocumentStore
	schemaStore   ports.SchemaStore
}

func NewService(documentStore ports.DocumentStore, schemaStore ports.SchemaStore) *Service {
	return &Service{
		documentStore: documentStore,
		schemaStore:   schemaStore,
	}
}

func (s *Service) IngestBatch(ctx context.Context, docs []domain.Document) error {
	if len(docs) == 0 {
		return nil
	}

	schema, err := s.schemaStore.GetSchema(ctx)
	if err != nil {
		return fmt.Errorf("get schema: %w", err)
	}

	facetMap := make(map[string]domain.FacetDef, len(schema.Facets))
	for _, f := range schema.Facets {
		facetMap[f.Key] = f
	}

	seenIDs := make(map[string]struct{}, len(docs))
	for i := range docs {
		if err := validateAndPrepareDoc(&docs[i], schema, facetMap, seenIDs); err != nil {
			return err
		}
	}

	err = s.documentStore.UpsertBatchWithOutbox(ctx, docs)
	if err != nil {
		return err
	}

	return nil
}

func validateAndPrepareDoc(doc *domain.Document, schema domain.CatalogSchema, facetMap map[string]domain.FacetDef, seenIDs map[string]struct{}) error {
	doc.ID = strings.TrimSpace(doc.ID)
	doc.Title = strings.TrimSpace(doc.Title)
	doc.Currency = strings.ToUpper(strings.TrimSpace(doc.Currency))
	if doc.ID == "" {
		return fmt.Errorf("%w: id is required", domain.ErrInvalidDocument)
	}
	if _, dup := seenIDs[doc.ID]; dup {
		return fmt.Errorf("%w: duplicate id %q in batch", domain.ErrInvalidDocument, doc.ID)
	}
	seenIDs[doc.ID] = struct{}{}
	if doc.Title == "" {
		return fmt.Errorf("%w: doc %q title is required", domain.ErrInvalidDocument, doc.ID)
	}
	if doc.Price < 0 {
		return fmt.Errorf("%w: doc %q price must be >= 0", domain.ErrInvalidDocument, doc.ID)
	}
	if doc.Currency == "" {
		doc.Currency = schema.Currency
	}
	if doc.Categories == nil {
		doc.Categories = []string{}
	}
	if doc.Attributes == nil {
		doc.Attributes = map[string]any{}
	}
	if doc.RAGContents == nil {
		doc.RAGContents = []string{}
	}
	if err := validateAttributes(doc.ID, doc.Attributes, facetMap); err != nil {
		return err
	}
	doc.ContentHash = doc.ComputeContentHash()
	doc.SyncStatus = domain.SyncStatusPending
	return nil
}

func validateAttributes(docID string, attrs map[string]any, facetMap map[string]domain.FacetDef) error {
	for k, val := range attrs {
		facet, ok := facetMap[k]
		if !ok {
			return fmt.Errorf("%w: doc %q has unknown attribute %q", domain.ErrInvalidDocument, docID, k)
		}
		switch facet.Type {
		case domain.FacetTypeEnum:
			if err := validateEnum(docID, k, val, facet.Values); err != nil {
				return err
			}
		case domain.FacetTypeNumber:
			if !isNumber(val) {
				return fmt.Errorf("%w: doc %q attribute %q must be a number", domain.ErrInvalidDocument, docID, k)
			}
		case domain.FacetTypeBool:
			if _, ok := val.(bool); !ok {
				return fmt.Errorf("%w: doc %q attribute %q must be a boolean", domain.ErrInvalidDocument, docID, k)
			}
		case domain.FacetTypeText:
			if _, ok := val.(string); !ok {
				return fmt.Errorf("%w: doc %q attribute %q must be a string", domain.ErrInvalidDocument, docID, k)
			}
		}
	}
	return nil
}

func validateEnum(docID, key string, val any, allowed []string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, v := range allowed {
		allowedSet[v] = struct{}{}
	}
	checkOne := func(s string) error {
		if _, ok := allowedSet[s]; !ok {
			return fmt.Errorf("%w: doc %q attribute %q value %q not in %v", domain.ErrInvalidDocument, docID, key, s, allowed)
		}
		return nil
	}
	switch v := val.(type) {
	case string:
		return checkOne(v)
	case []string:
		if len(v) == 0 {
			return fmt.Errorf("%w: doc %q attribute %q cannot be empty", domain.ErrInvalidDocument, docID, key)
		}
		for _, item := range v {
			if err := checkOne(item); err != nil {
				return err
			}
		}
		return nil
	case []any:
		if len(v) == 0 {
			return fmt.Errorf("%w: doc %q attribute %q cannot be empty", domain.ErrInvalidDocument, docID, key)
		}
		for _, raw := range v {
			s, ok := raw.(string)
			if !ok {
				return fmt.Errorf("%w: doc %q attribute %q elements must be string", domain.ErrInvalidDocument, docID, key)
			}
			if err := checkOne(s); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: doc %q attribute %q must be string or string array", domain.ErrInvalidDocument, docID, key)
	}
}
func isNumber(v any) bool {
	switch val := v.(type) {
	case int, int32, int64:
		return true
	case float32:
		return !math.IsNaN(float64(val)) && !math.IsInf(float64(val), 0)
	case float64:
		return !math.IsNaN(val) && !math.IsInf(val, 0)
	case json.Number:
		f, err := val.Float64()
		return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	default:
		return false
	}
}
