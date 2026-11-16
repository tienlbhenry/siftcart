package domain

import (
	"testing"
)

func Test_ComputeContentHash(t *testing.T) {
	doc := Document{
		ID:          "12344",
		Title:       "T-shirt",
		Price:       50000,
		Currency:    "VND",
		InStock:     true,
		Categories:  []string{"shirt", "cloth"},
		Attributes:  map[string]any{"color": "red", "size": "M"},
		RAGContents: []string{"This is a T-shirt", "summer T-shirt", "men T-shirt"},
	}

	priceChange := doc
	priceChange.Price = 60000
	priceChange.InStock = false
	if priceChange.ComputeContentHash() != doc.ComputeContentHash() {
		t.Error("Hashes changed when price/stock changed")
	}

	attributeChange := doc
	attributeChange.Attributes = map[string]any{"color": "blue", "size": "M"}
	if attributeChange.ComputeContentHash() == doc.ComputeContentHash() {
		t.Error("Hashes not changed when attributes changed")
	}

	updateTitle := doc
	updateTitle.Title = "New T-shirt"
	if updateTitle.ComputeContentHash() == doc.ComputeContentHash() {
		t.Error("Hashes not changed when title changed")
	}

	updateRAG := doc
	updateRAG.RAGContents = []string{"This is a T-shirt", "summer T-shirt", "men T-shirt", "new T-shirt"}
	if updateRAG.ComputeContentHash() == doc.ComputeContentHash() {
		t.Error("Hashes not changed when RAG contents changed")
	}
}
