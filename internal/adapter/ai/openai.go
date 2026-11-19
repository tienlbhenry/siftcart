package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"

	"github.com/tienlbhenry/siftcart/internal/domain"
	"github.com/tienlbhenry/siftcart/internal/ports"
)

type OpenAIProvider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	models     map[domain.ModelTier]string
}

var _ ports.Embedder = (*OpenAIProvider)(nil)

func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		baseURL: "https://api.openai.com/v1",
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		models: map[domain.ModelTier]string{
			domain.ModelTierFast:  "gpt-4o-mini",
			domain.ModelTierSmart: "gpt-4o",
		},
	}
}

func (o *OpenAIProvider) Name() string {
	return "OpenAI"
}

func (o *OpenAIProvider) newClient() openai.Client {
	options := []option.RequestOption{option.WithBaseURL(o.baseURL), option.WithHTTPClient(o.httpClient)}
	if o.apiKey != "" {
		options = append(options, option.WithAPIKey(o.apiKey))
	}
	return openai.NewClient(options...)
}

func (o *OpenAIProvider) Dimensions() int { return openAIEmbeddingDimensions }

func (o *OpenAIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	client := o.newClient()
	return embedOpenAICompatibleBatch(ctx, &client.Embeddings, o.Name(), openAIEmbeddingModel, texts)
}

func (o *OpenAIProvider) buildResponseParams(req domain.ChatRequest) (responses.ResponseNewParams, error) {
	if err := req.Validate(); err != nil {
		return responses.ResponseNewParams{}, err
	}

	model, ok := o.models[req.Tier]
	if !ok {
		return responses.ResponseNewParams{}, fmt.Errorf("unsupported model tier: %s", req.Tier)
	}

	var inputItems responses.ResponseInputParam
	for _, m := range req.Messages {
		switch m.Role {
		case domain.RoleSystem, domain.RoleUser:
			inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(
				m.Content, responses.EasyInputMessageRole(m.Role),
			))
		case domain.RoleAssistant:
			if m.Content != "" {
				inputItems = append(inputItems, responses.ResponseInputItemParamOfMessage(
					m.Content, responses.EasyInputMessageRoleAssistant,
				))
			}
			for _, tc := range m.ToolCalls {
				inputItems = append(inputItems, responses.ResponseInputItemParamOfFunctionCall(
					tc.ArgumentsJSON, tc.ID, tc.Name,
				))
			}
		case domain.RoleTool:
			item := responses.ResponseInputItemParamOfFunctionCallOutput(m.Content)
			item.OfFunctionCallOutput.CallID = openai.String(m.ToolCallID)
			inputItems = append(inputItems, item)
		}
	}

	params := responses.ResponseNewParams{
		Model: model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: inputItems},
	}

	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}
	if req.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(req.MaxTokens))
	}
	for _, tool := range req.Tools {
		var schema map[string]any
		if len(tool.ParametersJSON) == 0 {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		} else if err := json.Unmarshal(tool.ParametersJSON, &schema); err != nil || schema == nil {
			return responses.ResponseNewParams{}, fmt.Errorf("%w: parameters for tool %q must be a JSON object", domain.ErrInvalidAIRequest, tool.Name)
		}
		function := responses.ToolParamOfFunction(tool.Name, schema, false)
		if tool.Description != "" {
			function.OfFunction.Description = openai.String(tool.Description)
		}
		params.Tools = append(params.Tools, function)
	}
	if format := req.ResponseFormat; format != nil && format.Type == domain.ResponseFormatJSONSchema {
		var schema map[string]any
		if err := json.Unmarshal(format.JSONSchema, &schema); err != nil || schema == nil {
			return responses.ResponseNewParams{}, fmt.Errorf("%w: response schema must be a JSON object", domain.ErrInvalidAIRequest)
		}
		params.Text.Format = responses.ResponseFormatTextConfigParamOfJSONSchema(format.SchemaName, schema)
		params.Text.Format.OfJSONSchema.Strict = openai.Bool(true)
	}

	return params, nil
}

func (o *OpenAIProvider) Chat(ctx context.Context, req domain.ChatRequest) (domain.ChatResponse, error) {
	params, err := o.buildResponseParams(req)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	client := o.newClient()
	response, err := client.Responses.New(ctx, params)
	if err != nil {
		return domain.ChatResponse{}, fmt.Errorf("openai responses: %w", err)
	}
	if response.Status != responses.ResponseStatusCompleted {
		if response.Error.Message != "" {
			return domain.ChatResponse{}, fmt.Errorf("openai response %s: %s", response.Status, response.Error.Message)
		}
		return domain.ChatResponse{}, fmt.Errorf("openai response %s: %s", response.Status, response.IncompleteDetails.Reason)
	}

	result := domain.ChatResponse{
		Content:  response.OutputText(),
		Provider: o.Name(),
		Model:    response.Model,
		Usage: domain.TokenUsage{
			InputTokens:  int(response.Usage.InputTokens),
			OutputTokens: int(response.Usage.OutputTokens),
			TotalTokens:  int(response.Usage.TotalTokens),
		},
	}
	for _, item := range response.Output {
		if item.Type == "function_call" {
			call := item.AsFunctionCall()
			result.ToolCalls = append(result.ToolCalls, domain.ToolCall{
				ID:            call.CallID,
				Name:          call.Name,
				ArgumentsJSON: call.Arguments,
			})
		}
	}
	return result, nil
}

func (o *OpenAIProvider) ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.StreamChunk, <-chan error) {
	chunkCh := make(chan domain.StreamChunk, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(chunkCh)
		defer close(errCh)

		params, err := o.buildResponseParams(req)
		if err != nil {
			errCh <- fmt.Errorf("openai build input params: %w", err)
			return
		}

		client := o.newClient()

		stream := client.Responses.NewStreaming(ctx, params)
		defer stream.Close()

		var toolCalls []domain.ToolCall

		for stream.Next() {
			ev := stream.Current()
			switch ev.Type {
			case "response.output_text.delta":
				if ev.Delta == "" {
					continue
				}
				select {
				case chunkCh <- domain.StreamChunk{DeltaText: ev.Delta}:
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				}
			case "response.output_item.done":
				if ev.Item.Type == "function_call" {
					c := ev.Item.AsFunctionCall()
					toolCalls = append(toolCalls, domain.ToolCall{
						ID:            ev.Item.CallID,
						Name:          ev.Item.Name,
						ArgumentsJSON: c.Arguments,
					})
				}
			case "response.completed":
				usage := &domain.TokenUsage{
					InputTokens:  int(ev.Response.Usage.InputTokens),
					OutputTokens: int(ev.Response.Usage.OutputTokens),
					TotalTokens:  int(ev.Response.Usage.TotalTokens),
				}

				select {
				case chunkCh <- domain.StreamChunk{
					ToolCalls: toolCalls,
					Usage:     usage,
					Done:      true,
				}:
				case <-ctx.Done():
					errCh <- ctx.Err()
					return
				}
			case "response.failed", "error":
				errCh <- fmt.Errorf("%w: %s", domain.ErrAIProviderFailed, ev.Message)
				return
			}

		}
		if err := stream.Err(); err != nil {
			errCh <- fmt.Errorf("%w: %v", domain.ErrAIProviderFailed, err)
		}
	}()
	return chunkCh, errCh
}
