package prompts

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

func TestNewRegistryAndRenderEmbeddedPrompts(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	t.Run("query_parser v1.0.0", func(t *testing.T) {
		got, err := reg.Render("query_parser", "v1.0.0", map[string]any{
			"SchemaJSON": `{"facets":[{"name":"brand","type":"enum","values":["Dell"]}]}`,
			"Query":      "laptop Dell dưới 20 triệu",
		})
		if err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		if got.ModelTier != domain.ModelTierFast {
			t.Errorf("ModelTier = %q, want %q", got.ModelTier, domain.ModelTierFast)
		}
		if got.Temperature != 0.0 {
			t.Errorf("Temperature = %v, want 0.0", got.Temperature)
		}
		if !strings.Contains(got.System, "search query parser") {
			t.Errorf("unexpected System: %q", got.System)
		}
		if !strings.Contains(got.User, "laptop Dell dưới 20 triệu") {
			t.Errorf("User missing rendered query: %q", got.User)
		}
		if !strings.Contains(got.User, `"brand"`) {
			t.Errorf("User missing rendered schema: %q", got.User)
		}
	})

	t.Run("reranker v1.0.0", func(t *testing.T) {
		got, err := reg.Render("reranker", "v1.0.0", map[string]any{
			"Query":          "áo khoác đi mưa",
			"CandidatesJSON": `[{"doc_id":"p1","title":"Áo khoác chống nước"}]`,
		})
		if err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		if got.ModelTier != domain.ModelTierFast {
			t.Errorf("ModelTier = %q, want %q", got.ModelTier, domain.ModelTierFast)
		}
		if !strings.Contains(got.User, "áo khoác đi mưa") || !strings.Contains(got.User, "Áo khoác chống nước") {
			t.Errorf("unexpected User: %q", got.User)
		}
	})

	t.Run("copilot v1.0.0", func(t *testing.T) {
		got, err := reg.Render("copilot", "v1.0.0", map[string]any{
			"Query": "Tư vấn laptop học lập trình",
		})
		if err != nil {
			t.Fatalf("Render() error = %v", err)
		}
		if got.ModelTier != domain.ModelTierSmart {
			t.Errorf("ModelTier = %q, want %q", got.ModelTier, domain.ModelTierSmart)
		}
		if got.Temperature != 0.2 {
			t.Errorf("Temperature = %v, want 0.2", got.Temperature)
		}
		if !strings.Contains(got.System, "SiftCart Copilot") {
			t.Errorf("unexpected System: %q", got.System)
		}
		if got.User != "Tư vấn laptop học lập trình" {
			t.Errorf("User = %q, want %q", got.User, "Tư vấn laptop học lập trình")
		}
	})
}

func TestRegistryLookupAndRenderErrors(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	if _, err := reg.Render("unknown_prompt", "v1.0.0", nil); !errors.Is(err, ErrPromptNotFound) {
		t.Errorf("expected ErrPromptNotFound, got %v", err)
	}

	if _, err := reg.Render("query_parser", "v9.9.9", nil); !errors.Is(err, ErrPromptVersionNotFound) {
		t.Errorf("expected ErrPromptVersionNotFound, got %v", err)
	}

	if _, err := reg.Render("query_parser", "v1.0.0", map[string]any{"Query": "only query"}); err == nil {
		t.Errorf("expected error when template key SchemaJSON is missing")
	}
}

func TestNewRegistryFromFSValidation(t *testing.T) {
	badFS := fstest.MapFS{
		"demo/v1.0.0.yaml": &fstest.MapFile{
			Data: []byte(`
version: "v1.0.0"
description: "bad tier"
model_tier: "ultra"
temperature: 0.1
system_template: "sys"
user_template: "{{.Query}}"
`),
		},
	}

	if _, err := NewRegistryFromFS(badFS); !errors.Is(err, ErrInvalidPromptTemplate) {
		t.Errorf("expected ErrInvalidPromptTemplate, got %v", err)
	}
}
