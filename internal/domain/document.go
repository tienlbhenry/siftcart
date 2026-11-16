package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

type SyncStatusType string

const (
	SyncStatusPending SyncStatusType = "pending"
	SyncStatusIndexed SyncStatusType = "indexed"
	SyncStatusFailed  SyncStatusType = "failed"
)

type Document struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Price       int64          `json:"price"`
	Currency    string         `json:"currency"`
	InStock     bool           `json:"in_stock"`
	Categories  []string       `json:"categories"`
	Attributes  map[string]any `json:"attributes"`
	RAGContents []string       `json:"rag_contents"`
	ContentHash string         `json:"content_hash"`
	Version     int64          `json:"version"`
	SyncStatus  SyncStatusType `json:"sync_status"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type attrPair struct {
	Key   string `json:"k"`
	Value any    `json:"v"`
}

type hashPayload struct {
	Title       string     `json:"title"`
	Categories  []string   `json:"categories"`
	Attributes  []attrPair `json:"attributes"`
	RAGContents []string   `json:"rag_contents"`
}

func (d *Document) ComputeContentHash() string {
	categories := d.Categories
	if categories == nil {
		categories = []string{}
	}

	ragContents := d.RAGContents
	if ragContents == nil {
		ragContents = []string{}
	}

	payload := &hashPayload{
		Title:       d.Title,
		Categories:  categories,
		Attributes:  canonicalizeMap(d.Attributes),
		RAGContents: ragContents,
	}

	raw, _ := json.Marshal(payload)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func canonicalizeMap(data map[string]any) []attrPair {
	if len(data) == 0 {
		return []attrPair{}
	}

	pairs := make([]attrPair, 0, len(data))
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		pairs = append(pairs, attrPair{
			Key:   k,
			Value: canonicalizeValue(data[k]),
		})
	}
	return pairs
}

func canonicalizeValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return canonicalizeMap(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = canonicalizeValue(item)
		}
		return out
	default:
		return v
	}
}
