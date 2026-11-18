package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

type ModelTier string

const (
	ModelTierFast  ModelTier = "fast"
	ModelTierSmart ModelTier = "smart"
)

func (t ModelTier) IsValid() bool {
	switch t {
	case ModelTierFast, ModelTierSmart:
		return true
	default:
		return false
	}
}

type Role string

const (
	RoleUser      Role = "user"
	RoleSystem    Role = "system"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

func (r Role) IsValid() bool {
	switch r {
	case RoleUser, RoleSystem, RoleAssistant, RoleTool:
		return true
	default:
		return false
	}
}

type ToolDef struct {
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	ParametersJSON json.RawMessage `json:"parameters_json,omitempty"`
}

type ToolCall struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ArgumentsJSON string `json:"arguments_json"`
}

type ResponseFormatType string

const (
	ResponseFormatText       ResponseFormatType = "text"
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

func (t ResponseFormatType) IsValid() bool {
	switch t {
	case ResponseFormatText, ResponseFormatJSONSchema:
		return true
	default:
		return false
	}
}

type ResponseFormat struct {
	Type       ResponseFormatType `json:"type"`
	SchemaName string             `json:"schema_name,omitempty"`
	JSONSchema json.RawMessage    `json:"json_schema,omitempty"`
}

type ChatRequest struct {
	Tier           ModelTier       `json:"tier"`
	Messages       []ChatMessage   `json:"messages"`
	Tools          []ToolDef       `json:"tools,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
}

func (r ChatRequest) Validate() error {
	if !r.Tier.IsValid() {
		return fmt.Errorf("%w: invalid model tier %q", ErrInvalidAIRequest, r.Tier)
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("%w: messages cannot be empty", ErrInvalidAIRequest)
	}
	for i, msg := range r.Messages {
		if !msg.Role.IsValid() {
			return fmt.Errorf("%w: messages[%d] has invalid role %q", ErrInvalidAIRequest, i, msg.Role)
		}
	}
	for i, tool := range r.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("%w: tools[%d].name is required", ErrInvalidAIRequest, i)
		}
		if len(tool.ParametersJSON) > 0 && !json.Valid(tool.ParametersJSON) {
			return fmt.Errorf("%w: tools[%d].parameters_json must be valid JSON", ErrInvalidAIRequest, i)
		}
	}
	if r.ResponseFormat != nil {
		if !r.ResponseFormat.Type.IsValid() {
			return fmt.Errorf("%w: invalid response_format type %q", ErrInvalidAIRequest, r.ResponseFormat.Type)
		}
		if r.ResponseFormat.Type == ResponseFormatJSONSchema {
			if strings.TrimSpace(r.ResponseFormat.SchemaName) == "" {
				return fmt.Errorf("%w: response_format schema_name is required for json_schema", ErrInvalidAIRequest)
			}
			if len(r.ResponseFormat.JSONSchema) == 0 || !json.Valid(r.ResponseFormat.JSONSchema) {
				return fmt.Errorf("%w: response_format json_schema must be valid JSON", ErrInvalidAIRequest)
			}
		}
	}
	if r.Temperature != nil {
		temp := *r.Temperature
		if math.IsNaN(temp) || temp < 0 || temp > 2 {
			return fmt.Errorf("%w: temperature must be between 0 and 2", ErrInvalidAIRequest)
		}
	}
	if r.MaxTokens < 0 {
		return fmt.Errorf("%w: max_tokens must be >= 0", ErrInvalidAIRequest)
	}
	return nil
}

type StreamChunk struct {
	DeltaText string      `json:"delta_text,omitempty"`
	ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
	Usage     *TokenUsage `json:"usage,omitempty"`
	Done      bool        `json:"done"`
}

type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type ChatMessage struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ChatResponse struct {
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Provider  string     `json:"provider"`
	Model     string     `json:"model"`
	Usage     TokenUsage `json:"usage"`
	CostUSD   float64    `json:"cost_usd"`
}
