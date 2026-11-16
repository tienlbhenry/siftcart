CREATE TABLE IF NOT EXISTS catalog_schema (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    schema_json TEXT NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS documents (
     id TEXT PRIMARY KEY,
     title TEXT NOT NULL,
     price INTEGER NOT NULL DEFAULT 0,
     currency TEXT NOT NULL DEFAULT '',
     in_stock INTEGER NOT NULL DEFAULT 1,
     categories_json TEXT NOT NULL,
     attributes_json TEXT NOT NULL,
     rag_contents_json TEXT NOT NULL,
     content_hash TEXT NOT NULL,
     version INTEGER NOT NULL DEFAULT 1,
     sync_status TEXT NOT NULL,
     updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS outbox_events (
     id INTEGER PRIMARY KEY AUTOINCREMENT,
     doc_id TEXT NOT NULL,
     action TEXT NOT NULL,
     version INTEGER NOT NULL,
     payload_changed INTEGER NOT NULL,
     status TEXT NOT NULL,
     retry_count INTEGER NOT NULL DEFAULT 0,
     last_error TEXT NOT NULL DEFAULT '',
     created_at DATETIME NOT NULL,
     processed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_outbox_events_status_created
    ON outbox_events (status, created_at);