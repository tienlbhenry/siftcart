package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

type geminiTestTransport func(*http.Request) (*http.Response, error)

func (f geminiTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestGeminiStreamsAndReplaysToolCalls(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "gemini-test-key")
	provider := NewGeminiProvider("")
	provider.httpClient.Transport = geminiTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("x-goog-api-key") != "gemini-test-key" || req.Method != http.MethodPost {
			t.Error("unexpected request method or auth")
		}
		var payload geminiChatRequest
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		switch req.URL.Path {
		case "/v1beta/models/gemini-3.5-flash:streamGenerateContent":
			if req.URL.Query().Get("alt") != "sse" || payload.SystemInstruction == nil || len(payload.Tools) != 1 ||
				payload.GenerationConfig.ResponseMIMEType != "application/json" {
				t.Errorf("unexpected streaming request: %+v", payload)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(`data: {"candidates":[{"content":{"parts":[{"text":"internal","thought":true},{"text":"Checking."}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"stock_a","name":"stock","args":{"sku":"A"}},"thoughtSignature":"c2ln"},{"functionCall":{"name":"stock","args":{"sku":"B"}}}]},"finishReason":"STOP"}]}

data: {"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4,"thoughtsTokenCount":2,"totalTokenCount":16}}

`)),
			}, nil
		case "/v1beta/models/gemini-3.5-flash:generateContent":
			if len(payload.Contents) != 3 {
				t.Errorf("unexpected history: %+v", payload.Contents)
			} else {
				model, user := payload.Contents[1], payload.Contents[2]
				if model.Role != "model" || len(model.Parts) != 3 || model.Parts[1].ThoughtSignature != "c2ln" {
					t.Errorf("model history did not retain the signature: %+v", model)
				}
				var ids, outputs []string
				for _, part := range user.Parts {
					if part.FunctionResponse != nil {
						ids = append(ids, part.FunctionResponse.ID)
						outputs = append(outputs, string(part.FunctionResponse.Response))
					}
				}
				if user.Role != "user" || !slices.Equal(ids, []string{"stock_a", ""}) ||
					!slices.Equal(outputs, []string{`{"count":7}`, `{"output":"out of stock"}`}) {
					t.Errorf("unexpected tool results: ids=%v outputs=%v", ids, outputs)
				}
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"A has 7 left."}]},"finishReason":"STOP"}]}`)),
			}, nil
		default:
			t.Errorf("unexpected endpoint: %s", req.URL)
			return &http.Response{StatusCode: http.StatusNotFound, Body: http.NoBody}, nil
		}
	})

	req := domain.ChatRequest{
		Tier: domain.ModelTierSmart,
		Messages: []domain.ChatMessage{
			{Role: domain.RoleSystem, Content: "Reply based on stock."},
			{Role: domain.RoleUser, Content: "Check both SKUs."},
		},
		Tools: []domain.ToolDef{{Name: "stock", ParametersJSON: json.RawMessage(`{"type":"object"}`)}},
		ResponseFormat: &domain.ResponseFormat{
			Type: domain.ResponseFormatJSONSchema, SchemaName: "stock", JSONSchema: json.RawMessage(`{"type":"object"}`),
		},
	}
	chunks, errs := provider.ChatStream(context.Background(), req)
	var text string
	var final *domain.StreamChunk
	for chunk := range chunks {
		text += chunk.DeltaText
		if chunk.Done {
			final = &chunk
		}
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if text != "Checking." || final == nil || final.Usage == nil || final.Usage.OutputTokens != 6 || len(final.ToolCalls) != 2 {
		t.Fatalf("unexpected stream result: text=%q final=%+v", text, final)
	}
	calls := final.ToolCalls
	if calls[0].ID != "stock_a" || calls[0].ThoughtSignature != "c2ln" || calls[1].ID == "" {
		t.Fatalf("unexpected tool calls: %+v", calls)
	}
	req.Messages = append(req.Messages,
		domain.ChatMessage{Role: domain.RoleAssistant, Content: text, ToolCalls: calls},
		domain.ChatMessage{Role: domain.RoleTool, ToolCallID: calls[1].ID, Content: "out of stock"},
		domain.ChatMessage{Role: domain.RoleTool, ToolCallID: calls[0].ID, Content: `{"count":7}`},
	)
	response, err := provider.Chat(context.Background(), req)
	if err != nil || response.Content != "A has 7 left." {
		t.Fatalf("unexpected follow-up response: %+v, %v", response, err)
	}
}

func TestGeminiStreamEndsWithoutStop(t *testing.T) {
	provider := NewGeminiProvider("test-key")
	provider.httpClient.Transport = geminiTestTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"partial\"}]}}]}\n\n")),
		}, nil
	})
	chunks, errs := provider.ChatStream(context.Background(), domain.ChatRequest{
		Tier:     domain.ModelTierFast,
		Messages: []domain.ChatMessage{{Role: domain.RoleUser, Content: "Hello"}},
	})
	for chunk := range chunks {
		if chunk.Done {
			t.Error("truncated stream reported completion")
		}
	}
	if err := <-errs; !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected unexpected EOF, got %v", err)
	}
}

func TestGeminiEmbedBatchKeepsInputOrder(t *testing.T) {
	provider := NewGeminiProvider("test-key")
	var batchSizes []int
	provider.httpClient.Transport = geminiTestTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1beta/models/gemini-embedding-2:batchEmbedContents" {
			t.Errorf("unexpected embedding path: %s", req.URL.Path)
		}
		var payload struct {
			Requests []struct {
				Content              geminiContent `json:"content"`
				OutputDimensionality int           `json:"outputDimensionality"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			return nil, err
		}
		batchSizes = append(batchSizes, len(payload.Requests))
		type embedding struct {
			Values []float32 `json:"values"`
		}
		embeddings := make([]embedding, 0, len(payload.Requests))
		for _, input := range payload.Requests {
			if input.OutputDimensionality != provider.Dimensions() {
				t.Errorf("unexpected requested dimension: %d", input.OutputDimensionality)
			}
			n, err := strconv.Atoi(*input.Content.Parts[0].Text)
			if err != nil {
				return nil, err
			}
			values := make([]float32, provider.Dimensions())
			values[0] = float32(n)
			embeddings = append(embeddings, embedding{Values: values})
		}
		data, err := json.Marshal(map[string][]embedding{"embeddings": embeddings})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})
	texts := make([]string, 101)
	for i := range texts {
		texts[i] = strconv.Itoa(i)
	}
	vectors, err := provider.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(batchSizes, []int{100, 1}) || len(vectors) != len(texts) {
		t.Fatalf("unexpected batches %v and vector count %d", batchSizes, len(vectors))
	}
	for i, vector := range vectors {
		if len(vector) != provider.Dimensions() || vector[0] != float32(i) {
			t.Fatalf("unexpected vector at position %d", i)
		}
	}
}
