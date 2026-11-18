package ports

import (
	"context"

	"github.com/tienlbhenry/siftcart/internal/domain"
)

type LLMProvider interface {
	Name() string
	Chat(ctx context.Context, req domain.ChatRequest) (domain.ChatResponse, error)
	ChatStream(ctx context.Context, req domain.ChatRequest) (<-chan domain.StreamChunk, <-chan error)
}

type Embedder interface {
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
}
