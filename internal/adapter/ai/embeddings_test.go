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

	"github.com/openai/openai-go/v3/option"

	"github.com/tienlbhenry/siftcart/internal/domain"
	"github.com/tienlbhenry/siftcart/internal/ports"
)

type embeddingTestTransport func(*http.Request) (*http.Response, error)

func (f embeddingTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestEmbeddingProvidersBatchAndOrder(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "openai-test-key")
	t.Setenv("OPENAI_ORG_ID", "openai-test-org")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-test-key")

	for _, tc := range []struct {
		name, url, model, key, organization string
		newEmbedder                         func(*http.Client) ports.Embedder
	}{
		{
			name: "OpenAI", url: "https://api.openai.com/v1/embeddings", model: "text-embedding-3-small",
			key: "openai-test-key", organization: "openai-test-org",
			newEmbedder: func(client *http.Client) ports.Embedder {
				provider := NewOpenAIProvider("")
				provider.httpClient = client
				return provider
			},
		},
		{
			name: "OpenRouter", url: "https://openrouter.ai/api/v1/embeddings", model: "openai/text-embedding-3-small",
			key: "openrouter-test-key",
			newEmbedder: func(client *http.Client) ports.Embedder {
				provider := NewOpenRouterProvider("")
				provider.embeddings.Options = append(provider.embeddings.Options, option.WithHTTPClient(client))
				return provider
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var batchSizes []int
			client := &http.Client{Transport: embeddingTestTransport(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.String() != tc.url {
					t.Errorf("unexpected endpoint: %s %s", req.Method, req.URL)
				}
				if req.Header.Get("Authorization") != "Bearer "+tc.key || req.Header.Get("OpenAI-Organization") != tc.organization {
					t.Error("unexpected provider credentials")
				}
				var payload struct {
					Input          []string `json:"input"`
					Model          string   `json:"model"`
					Dimensions     int      `json:"dimensions"`
					EncodingFormat string   `json:"encoding_format"`
				}
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					return nil, err
				}
				if payload.Model != tc.model || payload.Dimensions != 768 || payload.EncodingFormat != "float" {
					t.Errorf("unexpected embedding options: %+v", payload)
				}

				batchSizes = append(batchSizes, len(payload.Input))
				type item struct {
					Index     int       `json:"index"`
					Embedding []float32 `json:"embedding"`
				}
				// The wire response may list results in a different order than the inputs.
				data := make([]item, 0, len(payload.Input))
				for i := len(payload.Input) - 1; i >= 0; i-- {
					n, err := strconv.Atoi(payload.Input[i])
					if err != nil {
						return nil, err
					}
					vector := make([]float32, 768)
					vector[0] = float32(n)
					data = append(data, item{Index: i, Embedding: vector})
				}
				response, err := json.Marshal(map[string][]item{"data": data})
				if err != nil {
					return nil, err
				}
				return embeddingTestResponse(http.StatusOK, string(response)), nil
			})}

			embedder := tc.newEmbedder(client)
			texts := make([]string, 33)
			for i := range texts {
				texts[i] = strconv.Itoa(i)
			}
			vectors, err := embedder.EmbedBatch(context.Background(), texts)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(batchSizes, []int{32, 1}) || len(vectors) != len(texts) {
				t.Fatalf("unexpected batches %v or vector count %d", batchSizes, len(vectors))
			}
			for i, vector := range vectors {
				if len(vector) != embedder.Dimensions() || vector[0] != float32(i) {
					t.Fatalf("unexpected embedding at input %d", i)
				}
			}

			if _, err := embedder.EmbedBatch(context.Background(), []string{"one", ""}); !errors.Is(err, domain.ErrInvalidAIRequest) {
				t.Fatalf("expected invalid input error, got %v", err)
			}
			empty, err := embedder.EmbedBatch(context.Background(), nil)
			if err != nil || len(empty) != 0 || len(batchSizes) != 2 {
				t.Fatalf("empty or invalid input made an unexpected request: batches=%v, error=%v", batchSizes, err)
			}
		})
	}
}

func TestOpenRouterEmbeddingErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    error
	}{
		{"rate limit", `{"error":{"message":"rate limited"}}`, 429, domain.ErrAIRateLimited},
		{"wrong dimensions", `{"data":[{"index":0,"embedding":[1,2]}]}`, 200, domain.ErrAIProviderFailed},
		{"missing index", `{"data":[{"embedding":[1,2]}]}`, 200, domain.ErrAIProviderFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := NewOpenRouterProvider("test-key")
			client := &http.Client{Transport: embeddingTestTransport(func(*http.Request) (*http.Response, error) {
				return embeddingTestResponse(tc.status, tc.body), nil
			})}
			provider.embeddings.Options = append(provider.embeddings.Options, option.WithHTTPClient(client))

			vectors, err := provider.EmbedBatch(context.Background(), []string{"one"})
			if vectors != nil || !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected no vectors and %v, got %v, %v", tc.wantErr, vectors, err)
			}
		})
	}
}

func embeddingTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
