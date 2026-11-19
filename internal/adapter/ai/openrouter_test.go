package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3/option"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

type openRouterTestTransport func(*http.Request) (*http.Response, error)

func (f openRouterTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestOpenRouterChatToolResult(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	t.Setenv("OPENAI_ORG_ID", "openai-test-org")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-test-key")
	provider := NewOpenRouterProvider("")
	provider.client.Options = append(provider.client.Options, option.WithHTTPClient(&http.Client{
		Transport: openRouterTestTransport(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost || req.URL.String() != "https://openrouter.ai/api/v1/chat/completions" {
				t.Errorf("unexpected request: %s %s", req.Method, req.URL)
			}
			if req.Header.Get("Authorization") != "Bearer openrouter-test-key" || req.Header.Get("OpenAI-Organization") != "" {
				t.Error("request did not isolate provider credentials")
			}
			var payload struct {
				Model    string `json:"model"`
				Messages []struct {
					Role       string `json:"role"`
					Content    string `json:"content"`
					ToolCallID string `json:"tool_call_id"`
					ToolCalls  []struct {
						ID string `json:"id"`
					} `json:"tool_calls"`
				} `json:"messages"`
				Provider struct {
					RequireParameters bool `json:"require_parameters"`
				} `json:"provider"`
			}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				return nil, err
			}
			if payload.Model != "openai/gpt-4.1" || !payload.Provider.RequireParameters ||
				len(payload.Messages) != 3 ||
				payload.Messages[1].Role != "assistant" || len(payload.Messages[1].ToolCalls) != 1 ||
				payload.Messages[1].ToolCalls[0].ID != "call_stock" ||
				payload.Messages[2].Role != "tool" || payload.Messages[2].ToolCallID != "call_stock" ||
				payload.Messages[2].Content != `{"quantity":7}` {
				t.Errorf("unexpected request payload: %+v", payload)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{
					"model":"openai/gpt-4.1",
					"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"7 in stock"}}],
					"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}
				}`)),
			}, nil
		}),
	}))
	response, err := provider.Chat(context.Background(), domain.ChatRequest{
		Tier:  domain.ModelTierSmart,
		Tools: []domain.ToolDef{{Name: "get_stock"}},
		Messages: []domain.ChatMessage{
			{Role: domain.RoleUser, Content: "Check stock"},
			{Role: domain.RoleAssistant, ToolCalls: []domain.ToolCall{
				{ID: "call_stock", Name: "get_stock", ArgumentsJSON: `{"sku":"A123"}`},
			}},
			{Role: domain.RoleTool, ToolCallID: "call_stock", Content: `{"quantity":7}`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "OpenRouter" || response.Content != "7 in stock" || response.Usage.TotalTokens != 25 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestOpenRouterChatStream(t *testing.T) {
	const textChunk = `data: {"id":"chat-1","choices":[{"index":0,"delta":{"content":"Checking"},"finish_reason":null}]}` + "\n\n"
	const toolChunks = `data: {"id":"chat-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_stock","arguments":"{\"sku\":\"A"}},{"index":1,"id":"call_b","type":"function","function":{"name":"get_stock","arguments":"{\"sku\":\"B"}}]}}]}

data: {"id":"chat-1","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2\"}"}},{"index":0,"function":{"arguments":"1\"}"}}]}}]}

data: {"id":"chat-1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"chat-1","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":8,"total_tokens":28}}

data: [DONE]

`
	for _, tc := range []struct {
		name, body string
		wantErr    error
	}{
		{name: "interleaved tool calls and final usage", body: textChunk + toolChunks},
		{name: "early EOF", body: textChunk, wantErr: io.ErrUnexpectedEOF},
		{name: "provider error after text", body: textChunk + `data: {"error":{"code":502,"message":"upstream failed"},"choices":[{"index":0,"finish_reason":"error","delta":{}}]}` + "\n\n", wantErr: domain.ErrAIProviderFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewOpenRouterProvider("test-key")
			provider.client.Options = append(provider.client.Options, option.WithHTTPClient(&http.Client{
				Transport: openRouterTestTransport(func(req *http.Request) (*http.Response, error) {
					var payload struct {
						Stream        bool `json:"stream"`
						StreamOptions struct {
							IncludeUsage bool `json:"include_usage"`
						} `json:"stream_options"`
					}
					if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
						return nil, err
					}
					if !payload.Stream || !payload.StreamOptions.IncludeUsage {
						t.Errorf("missing streaming options: %+v", payload)
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/event-stream"}},
						Body:       io.NopCloser(strings.NewReader(tc.body)),
					}, nil
				}),
			}))
			chunks, errs := provider.ChatStream(context.Background(), domain.ChatRequest{
				Tier:     domain.ModelTierFast,
				Tools:    []domain.ToolDef{{Name: "get_stock"}},
				Messages: []domain.ChatMessage{{Role: domain.RoleUser, Content: "Check stock"}},
			})
			var got []domain.StreamChunk
			for chunk := range chunks {
				got = append(got, chunk)
			}
			if err := <-errs; !errors.Is(err, tc.wantErr) {
				t.Fatalf("stream error = %v, want %v", err, tc.wantErr)
			}
			if len(got) == 0 || got[0].DeltaText != "Checking" || got[0].Done {
				t.Fatalf("unexpected text chunks: %+v", got)
			}
			if tc.wantErr != nil {
				if len(got) != 1 {
					t.Fatalf("unexpected chunks after text: %+v", got[1:])
				}
				return
			}
			if len(got) != 2 || !got[1].Done || got[1].Usage == nil || got[1].Usage.TotalTokens != 28 {
				t.Fatalf("unexpected final chunk: %+v", got)
			}
			calls := got[1].ToolCalls
			if len(calls) != 2 || calls[0].ID != "call_a" || calls[0].ArgumentsJSON != `{"sku":"A1"}` ||
				calls[1].ID != "call_b" || calls[1].ArgumentsJSON != `{"sku":"B2"}` {
				t.Fatalf("unexpected tool calls: %+v", calls)
			}
		})
	}
}
