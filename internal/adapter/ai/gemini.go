package ai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tienlbhenry/siftcart/internal/domain"
	"github.com/tienlbhenry/siftcart/internal/ports"
)

const (
	geminiEmbeddingModel      = "gemini-embedding-2"
	geminiEmbeddingDimensions = 768
	geminiEmbeddingBatchSize  = 100
	geminiLocalCallPrefix     = "gemini-local-"
)

type GeminiProvider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	models     map[domain.ModelTier]string
}

var (
	_ ports.LLMProvider = (*GeminiProvider)(nil)
	_ ports.Embedder    = (*GeminiProvider)(nil)
)

func NewGeminiProvider(apiKey string) *GeminiProvider {
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	return &GeminiProvider{
		baseURL:    "https://generativelanguage.googleapis.com/v1beta",
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		models: map[domain.ModelTier]string{
			domain.ModelTierFast:  "gemini-3.5-flash-lite",
			domain.ModelTierSmart: "gemini-3.5-flash",
		},
	}
}

func (g *GeminiProvider) Name() string { return "Gemini" }

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             *string                 `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"parametersJsonSchema,omitempty"`
}

type geminiTool struct {
	Functions []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiGenerationConfig struct {
	Temperature        *float64        `json:"temperature,omitempty"`
	MaxOutputTokens    int             `json:"maxOutputTokens,omitempty"`
	ResponseMIMEType   string          `json:"responseMimeType,omitempty"`
	ResponseJSONSchema json.RawMessage `json:"responseJsonSchema,omitempty"`
}

type geminiChatRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	Tools             []geminiTool           `json:"tools,omitempty"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig,omitzero"`
}

func buildGeminiChatRequest(req domain.ChatRequest) (geminiChatRequest, error) {
	var payload geminiChatRequest
	if err := req.Validate(); err != nil {
		return payload, err
	}

	payload.GenerationConfig.Temperature = req.Temperature
	payload.GenerationConfig.MaxOutputTokens = req.MaxTokens
	if format := req.ResponseFormat; format != nil && format.Type == domain.ResponseFormatJSONSchema {
		if !geminiJSONObject(format.JSONSchema) {
			return payload, fmt.Errorf("%w: Gemini response schema must be a JSON object", domain.ErrInvalidAIRequest)
		}
		payload.GenerationConfig.ResponseMIMEType = "application/json"
		payload.GenerationConfig.ResponseJSONSchema = format.JSONSchema
	}

	var declarations []geminiFunctionDeclaration
	for _, tool := range req.Tools {
		if len(tool.ParametersJSON) > 0 && !geminiJSONObject(tool.ParametersJSON) {
			return payload, fmt.Errorf("%w: parameters for tool %q must be a JSON object", domain.ErrInvalidAIRequest, tool.Name)
		}
		description := tool.Description
		if description == "" {
			description = tool.Name
		}
		declarations = append(declarations, geminiFunctionDeclaration{
			Name: tool.Name, Description: description, Schema: tool.ParametersJSON,
		})
	}
	if len(declarations) > 0 {
		payload.Tools = []geminiTool{{Functions: declarations}}
	}

	var systemParts []geminiPart
	var pendingCalls []domain.ToolCall
	for i := 0; i < len(req.Messages); i++ {
		msg := req.Messages[i]
		switch msg.Role {
		case domain.RoleSystem:
			systemParts = append(systemParts, geminiPart{Text: &msg.Content})
		case domain.RoleUser:
			payload.Contents = append(payload.Contents, geminiContent{
				Role: "user", Parts: []geminiPart{{Text: &msg.Content}},
			})
			pendingCalls = nil
		case domain.RoleAssistant:
			content := geminiContent{Role: "model"}
			if msg.Content != "" {
				content.Parts = append(content.Parts, geminiPart{Text: &msg.Content})
			}
			for _, call := range msg.ToolCalls {
				args := json.RawMessage(call.ArgumentsJSON)
				if len(args) > 0 && !geminiJSONObject(args) {
					return payload, fmt.Errorf("%w: arguments for tool %q must be a JSON object", domain.ErrInvalidAIRequest, call.Name)
				}
				content.Parts = append(content.Parts, geminiPart{
					ThoughtSignature: call.ThoughtSignature,
					FunctionCall:     &geminiFunctionCall{ID: geminiWireCallID(call.ID), Name: call.Name, Args: args},
				})
			}
			if len(content.Parts) > 0 {
				payload.Contents = append(payload.Contents, content)
				pendingCalls = msg.ToolCalls
			}
		case domain.RoleTool:
			start := i
			for i+1 < len(req.Messages) && req.Messages[i+1].Role == domain.RoleTool {
				i++
			}
			content, err := geminiToolResults(pendingCalls, req.Messages[start:i+1])
			if err != nil {
				return payload, err
			}
			payload.Contents = append(payload.Contents, content)
			pendingCalls = nil
		}
	}
	if len(systemParts) > 0 {
		payload.SystemInstruction = &geminiContent{Parts: systemParts}
	}
	if len(payload.Contents) == 0 {
		return payload, fmt.Errorf("%w: Gemini requires conversation content", domain.ErrInvalidAIRequest)
	}
	return payload, nil
}

func geminiJSONObject(data []byte) bool {
	data = bytes.TrimSpace(data)
	return len(data) > 0 && data[0] == '{' && json.Valid(data)
}

func geminiWireCallID(id string) string {
	if strings.HasPrefix(id, geminiLocalCallPrefix) {
		return ""
	}
	return id
}

func geminiToolResults(calls []domain.ToolCall, messages []domain.ChatMessage) (geminiContent, error) {
	content := geminiContent{Role: "user"}
	results := make(map[string]domain.ChatMessage, len(messages))
	for _, msg := range messages {
		if _, duplicate := results[msg.ToolCallID]; duplicate || msg.ToolCallID == "" {
			return content, fmt.Errorf("%w: missing or duplicate Gemini tool result ID %q", domain.ErrInvalidAIRequest, msg.ToolCallID)
		}
		results[msg.ToolCallID] = msg
	}
	if len(results) != len(calls) {
		return content, fmt.Errorf("%w: Gemini requires one result per tool call", domain.ErrInvalidAIRequest)
	}
	for _, call := range calls {
		msg, ok := results[call.ID]
		if !ok {
			return content, fmt.Errorf("%w: missing Gemini tool result for %q", domain.ErrInvalidAIRequest, call.ID)
		}
		delete(results, call.ID)
		output := json.RawMessage(msg.Content)
		if !geminiJSONObject(output) {
			if !json.Valid(output) {
				output, _ = json.Marshal(msg.Content)
			}
			output, _ = json.Marshal(map[string]json.RawMessage{"output": output})
		}
		content.Parts = append(content.Parts, geminiPart{FunctionResponse: &geminiFunctionResponse{
			ID: geminiWireCallID(call.ID), Name: call.Name, Response: output,
		}})
	}
	return content, nil
}

type geminiAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type geminiChatResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Usage *struct {
		PromptTokens   int `json:"promptTokenCount"`
		ResponseTokens int `json:"candidatesTokenCount"`
		ThoughtTokens  int `json:"thoughtsTokenCount"`
		TotalTokens    int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string          `json:"modelVersion"`
	Error        *geminiAPIError `json:"error"`
}

func geminiResponseError(status int, message string) error {
	kind := domain.ErrAIProviderFailed
	if status == http.StatusTooManyRequests {
		kind = domain.ErrAIRateLimited
	}
	if message != "" {
		return fmt.Errorf("%w: gemini status %d: %s", kind, status, message)
	}
	return fmt.Errorf("%w: gemini status %d", kind, status)
}

func (g *GeminiProvider) post(ctx context.Context, model, method string, payload any) (io.ReadCloser, error) {
	if model == "" {
		return nil, fmt.Errorf("%w: no Gemini model configured", domain.ErrInvalidAIRequest)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: gemini request: %w", domain.ErrInvalidAIRequest, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/models/"+model+":"+method, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: create Gemini request: %w", domain.ErrAIProviderFailed, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)
	if method == "streamGenerateContent?alt=sse" {
		req.Header.Set("Accept", "text/event-stream")
	}
	res, err := g.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: gemini request: %w", domain.ErrAIProviderFailed, err)
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		var failure struct {
			Error geminiAPIError `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&failure)
		return nil, geminiResponseError(res.StatusCode, failure.Error.Message)
	}
	return res.Body, nil
}

func (r geminiChatResponse) extract() (text, finish string, calls []domain.ToolCall, usage *domain.TokenUsage, err error) {
	if r.Error != nil {
		err = geminiResponseError(r.Error.Code, r.Error.Message)
		return
	}
	if r.PromptFeedback.BlockReason != "" && r.PromptFeedback.BlockReason != "BLOCK_REASON_UNSPECIFIED" {
		err = fmt.Errorf("%w: gemini blocked prompt: %s", domain.ErrAIProviderFailed, r.PromptFeedback.BlockReason)
		return
	}
	if r.Usage != nil {
		usage = &domain.TokenUsage{
			InputTokens:  r.Usage.PromptTokens,
			OutputTokens: r.Usage.ResponseTokens + r.Usage.ThoughtTokens,
			TotalTokens:  r.Usage.TotalTokens,
		}
	}
	if len(r.Candidates) == 0 {
		return
	}
	candidate := r.Candidates[0]
	finish = candidate.FinishReason
	if finish != "" && finish != "STOP" && finish != "FINISH_REASON_UNSPECIFIED" {
		err = fmt.Errorf("%w: gemini finishReason %s", domain.ErrAIProviderFailed, finish)
		return
	}

	var delta strings.Builder
	for _, part := range candidate.Content.Parts {
		if part.Text != nil && !part.Thought {
			delta.WriteString(*part.Text)
		}
		if part.FunctionCall != nil {
			call := part.FunctionCall
			id := call.ID
			if id == "" {
				id = geminiLocalCallPrefix + rand.Text()
			}
			args := string(call.Args)
			if args == "" || args == "null" {
				args = "{}"
			}
			if call.Name == "" || !geminiJSONObject([]byte(args)) {
				err = fmt.Errorf("%w: malformed Gemini function call", domain.ErrAIProviderFailed)
				return
			}
			calls = append(calls, domain.ToolCall{
				ID: id, Name: call.Name, ArgumentsJSON: args, ThoughtSignature: part.ThoughtSignature,
			})
		}
	}
	text = delta.String()
	return
}

func (g *GeminiProvider) Chat(ctx context.Context, req domain.ChatRequest) (domain.ChatResponse, error) {
	payload, err := buildGeminiChatRequest(req)
	if err != nil {
		return domain.ChatResponse{}, err
	}
	model := g.models[req.Tier]
	body, err := g.post(ctx, model, "generateContent", payload)
	if err != nil {
		return domain.ChatResponse{}, err
	}
	defer body.Close()
	var response geminiChatResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return domain.ChatResponse{}, fmt.Errorf("%w: decode Gemini response: %w", domain.ErrAIProviderFailed, err)
	}
	text, finish, calls, usage, err := response.extract()
	if err != nil {
		return domain.ChatResponse{}, err
	}
	if finish != "STOP" {
		return domain.ChatResponse{}, fmt.Errorf("%w: gemini response ended without STOP", domain.ErrAIProviderFailed)
	}
	if response.ModelVersion != "" {
		model = response.ModelVersion
	}
	result := domain.ChatResponse{Content: text, ToolCalls: calls, Provider: g.Name(), Model: model}
	if usage != nil {
		result.Usage = *usage
	}
	return result, nil
}

func (g *GeminiProvider) ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.StreamChunk, <-chan error) {
	chunks := make(chan domain.StreamChunk, 16)
	errs := make(chan error, 1)
	go func() {
		defer close(chunks)
		defer close(errs)
		if err := g.streamChat(ctx, req, chunks); err != nil {
			errs <- err
		}
	}()
	return chunks, errs
}

func (g *GeminiProvider) streamChat(ctx context.Context, req domain.ChatRequest, chunks chan<- domain.StreamChunk) error {
	payload, err := buildGeminiChatRequest(req)
	if err != nil {
		return err
	}
	body, err := g.post(ctx, g.models[req.Tier], "streamGenerateContent?alt=sse", payload)
	if err != nil {
		return err
	}
	defer body.Close()

	var completed bool
	final := domain.StreamChunk{Done: true}
	err = readGeminiSSE(body, func(data []byte) error {
		var response geminiChatResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return fmt.Errorf("%w: decode Gemini stream: %w", domain.ErrAIProviderFailed, err)
		}
		text, finish, calls, usage, err := response.extract()
		if err != nil {
			return err
		}
		completed = completed || finish == "STOP"
		final.ToolCalls = append(final.ToolCalls, calls...)
		if usage != nil {
			final.Usage = usage
		}
		if text != "" {
			select {
			case chunks <- domain.StreamChunk{DeltaText: text}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !completed {
		return fmt.Errorf("%w: gemini stream ended before STOP: %w", domain.ErrAIProviderFailed, io.ErrUnexpectedEOF)
	}
	select {
	case chunks <- final:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func readGeminiSSE(body io.Reader, onEvent func([]byte) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 16<<10), 4<<20)
	var data []byte
	for scanner.Scan() {
		line := scanner.Bytes()
		switch {
		case len(line) == 0 && len(data) > 0:
			if err := onEvent(data); err != nil {
				return err
			}
			data = data[:0]
		case bytes.HasPrefix(line, []byte("data:")):
			line = bytes.TrimPrefix(line[5:], []byte(" "))
			if len(data)+len(line) > 4<<20 {
				return fmt.Errorf("%w: Gemini SSE event too large", domain.ErrAIProviderFailed)
			}
			data = append(data, line...)
			data = append(data, '\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("%w: read Gemini stream: %w", domain.ErrAIProviderFailed, err)
	}
	if len(data) > 0 {
		return fmt.Errorf("%w: incomplete Gemini SSE event: %w", domain.ErrAIProviderFailed, io.ErrUnexpectedEOF)
	}
	return nil
}

func (g *GeminiProvider) Dimensions() int { return geminiEmbeddingDimensions }

func (g *GeminiProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += geminiEmbeddingBatchSize {
		end := min(start+geminiEmbeddingBatchSize, len(texts))
		batch, err := g.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("gemini embeddings at index %d: %w", start, err)
		}
		vectors = append(vectors, batch...)
	}
	return vectors, nil
}

func (g *GeminiProvider) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	type embeddingRequest struct {
		Model                string        `json:"model"`
		Content              geminiContent `json:"content"`
		OutputDimensionality int           `json:"outputDimensionality"`
	}
	payload := struct {
		Requests []embeddingRequest `json:"requests"`
	}{Requests: make([]embeddingRequest, 0, len(texts))}
	for _, text := range texts {
		payload.Requests = append(payload.Requests, embeddingRequest{
			Model:                "models/" + geminiEmbeddingModel,
			Content:              geminiContent{Parts: []geminiPart{{Text: &text}}},
			OutputDimensionality: g.Dimensions(),
		})
	}
	body, err := g.post(ctx, geminiEmbeddingModel, "batchEmbedContents", payload)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var response struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: decode Gemini embeddings: %w", domain.ErrAIProviderFailed, err)
	}
	if len(response.Embeddings) != len(texts) {
		return nil, fmt.Errorf("%w: gemini returned %d embeddings for %d inputs", domain.ErrAIProviderFailed, len(response.Embeddings), len(texts))
	}
	vectors := make([][]float32, 0, len(texts))
	for _, embedding := range response.Embeddings {
		if len(embedding.Values) != g.Dimensions() {
			return nil, fmt.Errorf("%w: gemini embedding has %d dimensions, expected %d", domain.ErrAIProviderFailed, len(embedding.Values), g.Dimensions())
		}
		vectors = append(vectors, embedding.Values)
	}
	return vectors, nil
}
