package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/openai/openai-go/v3"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

const (
	openAIEmbeddingModel      = "text-embedding-3-small"
	openAIEmbeddingDimensions = 768
	// 32 inputs at the model's 8,192-token limit fit within the 300,000-token request limit.
	openAIEmbeddingBatchSize = 32
)

func embedOpenAICompatibleBatch(ctx context.Context, service *openai.EmbeddingService, provider, model string, texts []string) ([][]float32, error) {
	for i, input := range texts {
		if input == "" {
			return nil, fmt.Errorf("%w: embedding input %d is empty", domain.ErrInvalidAIRequest, i)
		}
	}

	vectors := make([][]float32, len(texts))
	start := 0
	for batch := range slices.Chunk(texts, openAIEmbeddingBatchSize) {
		response, err := service.New(ctx, openai.EmbeddingNewParams{
			Model:          model,
			Input:          openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: batch},
			Dimensions:     openai.Int(openAIEmbeddingDimensions),
			EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
		})
		if err != nil {
			kind := domain.ErrAIProviderFailed
			var apiErr *openai.Error
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
				kind = domain.ErrAIRateLimited
			}
			return nil, fmt.Errorf("%w: %s embeddings at input %d: %w", kind, provider, start, err)
		}
		if response == nil || len(response.Data) != len(batch) {
			return nil, fmt.Errorf("%w: %s returned the wrong number of embeddings for batch at input %d", domain.ErrAIProviderFailed, provider, start)
		}
		batchVectors := vectors[start : start+len(batch)]
		for _, item := range response.Data {
			index := item.Index
			if !item.JSON.Index.Valid() || index < 0 || index >= int64(len(batch)) || batchVectors[index] != nil {
				return nil, fmt.Errorf("%w: %s returned a missing, invalid or duplicate embedding index at batch input %d", domain.ErrAIProviderFailed, provider, start)
			}
			if len(item.Embedding) != openAIEmbeddingDimensions {
				return nil, fmt.Errorf("%w: %s embedding at input %d has %d dimensions, expected %d", domain.ErrAIProviderFailed, provider, start+int(index), len(item.Embedding), openAIEmbeddingDimensions)
			}
			vector := make([]float32, len(item.Embedding))
			for i, value := range item.Embedding {
				vector[i] = float32(value)
			}
			batchVectors[index] = vector
		}
		start += len(batch)
	}
	return vectors, nil
}
