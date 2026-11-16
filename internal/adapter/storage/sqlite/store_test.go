package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tienlbhenry/siftcart/internal/domain"
	"github.com/tienlbhenry/siftcart/internal/ports"
)

var (
	_ ports.SchemaStore   = (*Store)(nil)
	_ ports.DocumentStore = (*Store)(nil)
	_ ports.OutboxStore   = (*Store)(nil)
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	db, err := OpenDB(ctx, ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return NewStore(db)
}

func TestStore_Schema(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Empty DB -> ErrSchemaNotFound
	if _, err := store.GetSchema(ctx); !errors.Is(err, domain.ErrSchemaNotFound) {
		t.Fatalf("expected ErrSchemaNotFound, got %v", err)
	}

	first := domain.CatalogSchema{
		ShopName:   "Saigon Streetwear",
		Currency:   "VND",
		WebhookURL: "https://shop.example.com/hook",
		Facets: []domain.FacetDef{
			{Key: "size", Type: domain.FacetTypeEnum, Values: []string{"M", "L"}, Filterable: true},
		},
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := store.UpsertSchema(ctx, first); err != nil {
		t.Fatalf("upsert first schema: %v", err)
	}

	got, err := store.GetSchema(ctx)
	if err != nil {
		t.Fatalf("get schema: %v", err)
	}
	if got.ShopName != first.ShopName || got.Currency != first.Currency || len(got.Facets) != 1 {
		t.Fatalf("unexpected schema: %+v", got)
	}

	// Upsert again overwrites the single row (id = 1)
	updated := first
	updated.ShopName = "TechGear VN"
	updated.Facets = []domain.FacetDef{
		{Key: "ram_gb", Type: domain.FacetTypeNumber, Filterable: true},
	}
	if err := store.UpsertSchema(ctx, updated); err != nil {
		t.Fatalf("upsert updated schema: %v", err)
	}

	got, err = store.GetSchema(ctx)
	if err != nil {
		t.Fatalf("get updated schema: %v", err)
	}
	if got.ShopName != "TechGear VN" || len(got.Facets) != 1 || got.Facets[0].Key != "ram_gb" {
		t.Fatalf("unexpected updated schema: %+v", got)
	}
}

func TestStore_DocumentsAndOutboxPayloadChanged(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if err := store.UpsertBatchWithOutbox(ctx, nil); err != nil {
		t.Fatalf("expected nil on empty batch, got %v", err)
	}

	if _, err := store.GetByID(ctx, "   "); !errors.Is(err, domain.ErrInvalidDocument) {
		t.Fatalf("expected ErrInvalidDocument for empty id, got %v", err)
	}
	if _, err := store.GetByID(ctx, "missing"); !errors.Is(err, domain.ErrDocumentNotFound) {
		t.Fatalf("expected ErrDocumentNotFound, got %v", err)
	}

	doc1 := domain.Document{
		ID:          "sku-1",
		Title:       "Áo khoác chống nước",
		Price:       850000,
		Currency:    "VND",
		InStock:     true,
		Categories:  []string{"Áo khoác"},
		Attributes:  map[string]any{"size": "L", "waterproof": true},
		RAGContents: []string{"Phù hợp đi Đà Lạt"},
	}
	doc1.ContentHash = doc1.ComputeContentHash()

	doc2 := domain.Document{
		ID:       "sku-2",
		Title:    "Áo thun cơ bản",
		Price:    250000,
		Currency: "VND",
		InStock:  false,
	}
	doc2.ContentHash = doc2.ComputeContentHash()

	if err := store.UpsertBatchWithOutbox(ctx, []domain.Document{doc1, doc2}); err != nil {
		t.Fatalf("upsert initial batch: %v", err)
	}

	got1, err := store.GetByID(ctx, "sku-1")
	if err != nil {
		t.Fatalf("get sku-1: %v", err)
	}
	if got1.Version != 1 || got1.SyncStatus != domain.SyncStatusPending || !got1.InStock {
		t.Fatalf("unexpected sku-1 state: %+v", got1)
	}
	if len(got1.Categories) != 1 || got1.Attributes["size"] != "L" || len(got1.RAGContents) != 1 {
		t.Fatalf("unexpected sku-1 json fields: %+v", got1)
	}

	got2, err := store.GetByID(ctx, "sku-2")
	if err != nil {
		t.Fatalf("get sku-2: %v", err)
	}
	if got2.InStock || got2.Categories == nil || got2.Attributes == nil || got2.RAGContents == nil {
		t.Fatalf("expected normalized non-nil slices/map and InStock=false: %+v", got2)
	}

	events, err := store.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 pending events, got %d", len(events))
	}
	if events[0].DocID != "sku-1" || events[1].DocID != "sku-2" || !events[0].PayloadChanged || events[0].Version != 1 {
		t.Fatalf("unexpected initial events: %+v", events)
	}

	// Update only Price & InStock -> same ContentHash -> PayloadChanged must be false
	doc1PriceOnly := doc1
	doc1PriceOnly.Price = 790000
	doc1PriceOnly.InStock = false
	doc1PriceOnly.ContentHash = doc1PriceOnly.ComputeContentHash()

	if err := store.UpsertBatchWithOutbox(ctx, []domain.Document{doc1PriceOnly}); err != nil {
		t.Fatalf("upsert price-only update: %v", err)
	}

	events, err = store.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending after price update: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	last := events[2]
	if last.DocID != "sku-1" || last.Version != 2 || last.PayloadChanged {
		t.Fatalf("expected version=2 payload_changed=false, got %+v", last)
	}

	// Update RAGContents -> ContentHash changes -> PayloadChanged must be true
	doc1TextChanged := doc1PriceOnly
	doc1TextChanged.RAGContents = []string{"Chống nước tuyệt đối, giữ ấm tốt"}
	doc1TextChanged.ContentHash = doc1TextChanged.ComputeContentHash()

	if err := store.UpsertBatchWithOutbox(ctx, []domain.Document{doc1TextChanged}); err != nil {
		t.Fatalf("upsert text update: %v", err)
	}

	events, err = store.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending after text update: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	last = events[3]
	if last.Version != 3 || !last.PayloadChanged {
		t.Fatalf("expected version=3 payload_changed=true, got %+v", last)
	}
}

func TestStore_OutboxLifecycleAndDelete(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Non-positive limit returns empty slice cleanly
	empty, err := store.FetchPending(ctx, 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("expected empty slice for limit=0, got len=%d err=%v", len(empty), err)
	}
	empty, err = store.FetchPending(ctx, -5)
	if err != nil || len(empty) != 0 {
		t.Fatalf("expected empty slice for limit=-5, got len=%d err=%v", len(empty), err)
	}

	doc := domain.Document{ID: "sku-del", Title: "Laptop Dell", Price: 20000000, Currency: "VND", InStock: true}
	doc.ContentHash = doc.ComputeContentHash()
	if err := store.UpsertBatchWithOutbox(ctx, []domain.Document{doc}); err != nil {
		t.Fatalf("upsert doc: %v", err)
	}

	events, err := store.FetchPending(ctx, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("expected 1 pending event, got len=%d err=%v", len(events), err)
	}

	// MarkDone removes it from pending
	if err := store.MarkDone(ctx, events[0].ID); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if err := store.MarkDone(ctx, 999999); err == nil {
		t.Fatal("expected error marking non-existent event done")
	}

	pendingAfterDone, err := store.FetchPending(ctx, 10)
	if err != nil || len(pendingAfterDone) != 0 {
		t.Fatalf("expected 0 pending events after MarkDone, got len=%d err=%v", len(pendingAfterDone), err)
	}

	// DeleteWithOutbox on missing doc -> ErrDocumentNotFound
	if err := store.DeleteWithOutbox(ctx, "unknown"); !errors.Is(err, domain.ErrDocumentNotFound) {
		t.Fatalf("expected ErrDocumentNotFound, got %v", err)
	}

	// DeleteWithOutbox on existing doc -> deletes row and inserts delete outbox event
	if err := store.DeleteWithOutbox(ctx, "sku-del"); err != nil {
		t.Fatalf("delete doc: %v", err)
	}
	if _, err := store.GetByID(ctx, "sku-del"); !errors.Is(err, domain.ErrDocumentNotFound) {
		t.Fatalf("expected ErrDocumentNotFound after delete, got %v", err)
	}

	events, err = store.FetchPending(ctx, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("expected 1 delete event, got len=%d err=%v", len(events), err)
	}
	delEvt := events[0]
	if delEvt.Action != domain.OutboxEventActionDelete || delEvt.Version != 2 || delEvt.PayloadChanged {
		t.Fatalf("unexpected delete outbox event: %+v", delEvt)
	}

	// MarkFailed updates status, last_error, retry_count and excludes from pending
	if err := store.MarkFailed(ctx, delEvt.ID, "es timeout"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := store.MarkFailed(ctx, 999999, "nope"); err == nil {
		t.Fatal("expected error marking non-existent event failed")
	}

	pendingAfterFail, err := store.FetchPending(ctx, 10)
	if err != nil || len(pendingAfterFail) != 0 {
		t.Fatalf("expected 0 pending events after MarkFailed, got len=%d err=%v", len(pendingAfterFail), err)
	}

	var status, lastErr string
	var retryCount int
	err = store.db.QueryRowContext(ctx, `SELECT status, last_error, retry_count FROM outbox_events WHERE id = ?`, delEvt.ID).
		Scan(&status, &lastErr, &retryCount)
	if err != nil {
		t.Fatalf("inspect failed event: %v", err)
	}
	if status != string(domain.OutboxEventStatusFailed) || lastErr != "es timeout" || retryCount != 1 {
		t.Fatalf("unexpected failed event row: status=%q err=%q retry=%d", status, lastErr, retryCount)
	}
}

func TestStore_UpsertBatchRollbackOnError(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	validDoc := domain.Document{ID: "sku-ok", Title: "Valid Doc", Price: 1000, Currency: "VND"}
	validDoc.ContentHash = validDoc.ComputeContentHash()

	badDoc := domain.Document{
		ID:         "sku-bad",
		Title:      "Bad Doc",
		Price:      1000,
		Currency:   "VND",
		Attributes: map[string]any{"invalid": func() {}}, // triggers json.Marshal error mid-transaction
	}

	err := store.UpsertBatchWithOutbox(ctx, []domain.Document{validDoc, badDoc})
	if err == nil {
		t.Fatal("expected error when batch contains unmarshalable attribute")
	}

	// First doc must be rolled back along with its outbox event
	if _, err := store.GetByID(ctx, "sku-ok"); !errors.Is(err, domain.ErrDocumentNotFound) {
		t.Fatalf("expected sku-ok to be rolled back, got err=%v", err)
	}
	events, err := store.FetchPending(ctx, 10)
	if err != nil || len(events) != 0 {
		t.Fatalf("expected 0 outbox events after rollback, got len=%d err=%v", len(events), err)
	}
}
