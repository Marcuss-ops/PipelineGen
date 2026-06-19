// Package ai provides canonical interfaces for AI/ML operations.
//
// Implementations live in:
//   - internal/platform/ai/ollama/ (LLM client)
//   - internal/platform/ai/vlm/   (vision-language model)
//   - internal/platform/ai/reranker/ (cross-encoder reranker)
//
// New code should depend on these interfaces, not the concrete clients.
package ai

import "context"

// LLMClient is the contract for text generation via LLM.
type LLMClient interface {
	Chat(ctx context.Context, messages []Message, options map[string]any) (string, error)
	Generate(ctx context.Context, prompt string) (string, error)
	Embed(ctx context.Context, prompt string) ([]float32, error)
	CheckHealth(ctx context.Context) bool
}

// Message represents a chat message.
type Message struct {
	Role    string
	Content string
}

// RerankerClient is the contract for cross-encoder reranking.
type RerankerClient interface {
	Rerank(ctx context.Context, query string, candidates []RerankCandidate) ([]RerankResult, error)
	IsEnabled() bool
}

// RerankCandidate is an input to reranking.
type RerankCandidate struct {
	ID   string
	Text string
}

// RerankResult is a single reranked result.
type RerankResult struct {
	ID          string
	RerankScore float64
}
