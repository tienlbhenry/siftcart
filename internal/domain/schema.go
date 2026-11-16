package domain

import (
	"fmt"
	"regexp"
	"time"
)

type FacetType string

const (
	FacetTypeEnum   FacetType = "enum"
	FacetTypeNumber FacetType = "number"
	FacetTypeBool   FacetType = "bool"
	FacetTypeText   FacetType = "text"
)

type FacetDef struct {
	Key         string    `json:"key"`
	Type        FacetType `json:"type"`
	Description string    `json:"description"`
	Values      []string  `json:"values,omitempty"`
	Filterable  bool      `json:"filterable"`
}

func (t FacetType) IsValid() bool {
	switch t {
	case FacetTypeEnum, FacetTypeNumber, FacetTypeBool, FacetTypeText:
		return true
	default:
		return false
	}
}

type CatalogSchema struct {
	ShopName   string     `json:"shop_name"`
	Currency   string     `json:"currency"`
	WebhookURL string     `json:"webhook_url"`
	Facets     []FacetDef `json:"facets"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

var validFacetKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (c *CatalogSchema) Validate() error {
	if c.ShopName == "" {
		return fmt.Errorf("shop name is required")
	}

	if c.Currency == "" {
		return fmt.Errorf("currency is required")
	}

	keys := make(map[string]bool)
	for _, e := range c.Facets {
		if e.Key == "" {
			return fmt.Errorf("facet key is required")
		}

		if e.Type == FacetTypeEnum && len(e.Values) == 0 {
			return fmt.Errorf("facet values are required")
		}

		if !validFacetKey.MatchString(e.Key) {
			return fmt.Errorf("invalid facet key: %s", e.Key)
		}

		if !e.Type.IsValid() {
			return fmt.Errorf("invalid facet type: %s", e.Type)
		}

		if keys[e.Key] == true {
			return fmt.Errorf("duplicate facet key: %s", e.Key)
		}
		keys[e.Key] = true

	}
	return nil
}
