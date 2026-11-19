package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/tienlbhenry/siftcart/internal/domain"
	"github.com/tienlbhenry/siftcart/internal/ports"
)

type OpenRouterProvider struct {
	client     openai.ChatCompletionService
	embeddings openai.EmbeddingService
	models     map[domain.ModelTier]string
}

var (
	_ ports.LLMProvider = (*OpenRouterProvider)(nil)
	_ ports.Embedder    = (*OpenRouterProvider)(nil)
)

func NewOpenRouterProvider(apiKey string) *OpenRouterProvider {
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}

	// Use the services directly so OpenAI credentials and headers are never inherited.
	options := []option.RequestOption{
		option.WithBaseURL("https://openrouter.ai/api/v1"),
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Timeout: 30 * time.Second}),
		option.WithHeader("X-OpenRouter-Title", "SiftCart"),
		option.WithMaxRetries(0),
	}
	return &OpenRouterProvider{
		client:     openai.NewChatCompletionService(options...),
		embeddings: openai.NewEmbeddingService(options...),
		models: map[domain.ModelTier]string{
			domain.ModelTierFast:  "openai/gpt-4.1-mini",
			domain.ModelTierSmart: "openai/gpt-4.1",
		},
	}
}

func (o *OpenRouterProvider) Name() string {
	return "OpenRouter"
}

func (o *OpenRouterProvider) Dimensions() int { return openAIEmbeddingDimensions }

func (o *OpenRouterProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return embedOpenAICompatibleBatch(ctx, &o.embeddings, o.Name(), "openai/"+openAIEmbeddingModel, texts)
}

func (o *OpenRouterProvider) buildChatParams(req domain.ChatRequest) (openai.ChatCompletionNewParams, error) {
	if err := req.Validate(); err != nil {
		return openai.ChatCompletionNewParams{}, err
	}

	model, ok := o.models[req.Tier]
	if !ok {
		return openai.ChatCompletionNewParams{}, fmt.Errorf("%w: unsupported model tier %q", domain.ErrInvalidAIRequest, req.Tier)
	}

	params := openai.ChatCompletionNewParams{
		Model:    model,
		Messages: make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages)),
	}
	for _, message := range req.Messages {
		var item openai.ChatCompletionMessageParamUnion
		switch message.Role {
		case domain.RoleSystem:
			item = openai.SystemMessage(message.Content)
		case domain.RoleUser:
			item = openai.UserMessage(message.Content)
		case domain.RoleAssistant:
			item = openai.ChatCompletionMessageParamOfAssistant(message.Content)
			for _, call := range message.ToolCalls {
				item.OfAssistant.ToolCalls = append(item.OfAssistant.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: call.ID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      call.Name,
							Arguments: call.ArgumentsJSON,
						},
					},
				})
			}
		case domain.RoleTool:
			item = openai.ToolMessage(message.Content, message.ToolCallID)
		}
		params.Messages = append(params.Messages, item)
	}

	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}
	for _, tool := range req.Tools {
		function := shared.FunctionDefinitionParam{Name: tool.Name}
		if tool.Description != "" {
			function.Description = openai.String(tool.Description)
		}
		if len(tool.ParametersJSON) == 0 {
			function.Parameters = shared.FunctionParameters{"type": "object", "properties": map[string]any{}}
		} else if err := json.Unmarshal(tool.ParametersJSON, &function.Parameters); err != nil || function.Parameters == nil {
			return openai.ChatCompletionNewParams{}, fmt.Errorf("%w: parameters for tool %q must be a JSON object", domain.ErrInvalidAIRequest, tool.Name)
		}
		params.Tools = append(params.Tools, openai.ChatCompletionFunctionTool(function))
	}
	if format := req.ResponseFormat; format != nil && format.Type == domain.ResponseFormatJSONSchema {
		var schema map[string]any
		if err := json.Unmarshal(format.JSONSchema, &schema); err != nil || schema == nil {
			return openai.ChatCompletionNewParams{}, fmt.Errorf("%w: response schema must be a JSON object", domain.ErrInvalidAIRequest)
		}
		params.ResponseFormat.OfJSONSchema = &shared.ResponseFormatJSONSchemaParam{
			JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   format.SchemaName,
				Schema: schema,
				Strict: openai.Bool(true),
			},
		}
	}

	return params, nil
}

func openRouterRequestOptions(params openai.ChatCompletionNewParams) []option.RequestOption {
	if len(params.Tools) > 0 || params.ResponseFormat.OfJSONSchema != nil {
		return []option.RequestOption{option.WithJSONSet("provider.require_parameters", true)}
	}
	return nil
}

func (o *OpenRouterProvider) Chat(ctx context.Context, req domain.ChatRequest) (domain.ChatResponse, error) {
	params, err := o.buildChatParams(req)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	response, err := o.client.New(ctx, params, openRouterRequestOptions(params)...)
	if err != nil {
		return domain.ChatResponse{}, openRouterRequestError(err)
	}
	if response == nil {
		return domain.ChatResponse{}, fmt.Errorf("%w: empty openrouter response", domain.ErrAIProviderFailed)
	}
	if field := response.JSON.ExtraFields["error"]; field.Valid() {
		return domain.ChatResponse{}, fmt.Errorf("%w: openrouter: %s", domain.ErrAIProviderFailed, field.Raw())
	}
	if len(response.Choices) == 0 {
		return domain.ChatResponse{}, fmt.Errorf("%w: openrouter returned no choices", domain.ErrAIProviderFailed)
	}
	choice := response.Choices[0]
	if err := checkOpenRouterFinish(choice.FinishReason); err != nil {
		return domain.ChatResponse{}, err
	}

	return domain.ChatResponse{
		Content:   choice.Message.Content,
		ToolCalls: openRouterToolCalls(choice.Message.ToolCalls),
		Provider:  o.Name(),
		Model:     response.Model,
		Usage:     openRouterUsage(response.Usage),
	}, nil
}

func (o *OpenRouterProvider) ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.StreamChunk, <-chan error) {
	chunkCh := make(chan domain.StreamChunk, 16)
	errCh := make(chan error, 1)
	go func() {
		defer close(chunkCh)
		defer close(errCh)

		if err := o.streamChat(ctx, req, chunkCh); err != nil {
			errCh <- err
		}
	}()
	return chunkCh, errCh
}

func (o *OpenRouterProvider) streamChat(ctx context.Context, req domain.ChatRequest, chunkCh chan<- domain.StreamChunk) error {
	params, err := o.buildChatParams(req)
	if err != nil {
		return err
	}

	params.StreamOptions.IncludeUsage = openai.Bool(true)
	stream := o.client.NewStreaming(ctx, params, openRouterRequestOptions(params)...)
	defer stream.Close()

	var accumulated openai.ChatCompletionAccumulator
	var usage *domain.TokenUsage
	for stream.Next() {
		chunk := stream.Current()
		if !accumulated.AddChunk(chunk) {
			return fmt.Errorf("%w: invalid openrouter stream chunk", domain.ErrAIProviderFailed)
		}
		if chunk.JSON.Usage.Valid() {
			tokens := openRouterUsage(chunk.Usage)
			usage = &tokens
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.FinishReason != "" {
				if err := checkOpenRouterFinish(choice.FinishReason); err != nil {
					return err
				}
			}
			if choice.Delta.Content != "" {
				select {
				case chunkCh <- domain.StreamChunk{DeltaText: choice.Delta.Content}:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
	if err := stream.Err(); err != nil {
		return openRouterRequestError(err)
	}
	if len(accumulated.Choices) == 0 || accumulated.Choices[0].FinishReason == "" {
		return fmt.Errorf("%w: openrouter stream ended before finish_reason: %w", domain.ErrAIProviderFailed, io.ErrUnexpectedEOF)
	}

	select {
	case chunkCh <- domain.StreamChunk{
		ToolCalls: openRouterToolCalls(accumulated.Choices[0].Message.ToolCalls),
		Usage:     usage,
		Done:      true,
	}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func checkOpenRouterFinish(reason string) error {
	switch reason {
	case "stop", "tool_calls":
		return nil
	default:
		return fmt.Errorf("%w: openrouter finish_reason %q", domain.ErrAIProviderFailed, reason)
	}
}

func openRouterRequestError(err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: openrouter: %w", domain.ErrAIRateLimited, err)
	}
	return fmt.Errorf("%w: openrouter: %w", domain.ErrAIProviderFailed, err)
}

func openRouterToolCalls(calls []openai.ChatCompletionMessageToolCallUnion) []domain.ToolCall {
	var result []domain.ToolCall
	for _, call := range calls {
		if call.Type == "function" {
			result = append(result, domain.ToolCall{
				ID:            call.ID,
				Name:          call.Function.Name,
				ArgumentsJSON: call.Function.Arguments,
			})
		}
	}
	return result
}

func openRouterUsage(usage openai.CompletionUsage) domain.TokenUsage {
	return domain.TokenUsage{
		InputTokens:  int(usage.PromptTokens),
		OutputTokens: int(usage.CompletionTokens),
		TotalTokens:  int(usage.TotalTokens),
	}
}
