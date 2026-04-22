package scriptdocs

import (
	"context"
	"math"
	"sync"
	"velox/go-master/internal/ml/ollama"
)

// SemanticScorer computes similarity between phrases and concepts using embeddings.
type SemanticScorer struct {
	generator *ollama.Generator
	cache     map[string][]float32
	mu        sync.RWMutex
}

// NewSemanticScorer creates a new scorer.
func NewSemanticScorer(gen *ollama.Generator) *SemanticScorer {
	return &SemanticScorer{
		generator: gen,
		cache:     make(map[string][]float32),
	}
}

// ScoreSimilarity returns a score from 0 to 10 based on semantic similarity.
func (s *SemanticScorer) ScoreSimilarity(ctx context.Context, phrase, keyword string) int {
	if s.generator == nil {
		return 0
	}

	phraseEmbed, err := s.getEmbedding(ctx, phrase)
	if err != nil {
		return 0
	}

	keywordEmbed, err := s.getEmbedding(ctx, keyword)
	if err != nil {
		return 0
	}

	sim := cosineSimilarity(phraseEmbed, keywordEmbed)
	
	// Convert cosine similarity (typically 0.4 - 0.9 for related text) to 0-10 score
	if sim < 0.5 {
		return 0
	}
	
	score := int((sim - 0.5) * 20) // 0.5 -> 0, 1.0 -> 10
	if score > 10 {
		score = 10
	}
	return score
}

func (s *SemanticScorer) getEmbedding(ctx context.Context, text string) ([]float32, error) {
	s.mu.RLock()
	if embed, ok := s.cache[text]; ok {
		s.mu.RUnlock()
		return embed, nil
	}
	s.mu.RUnlock()

	// Call Ollama client Embed (which I just added)
	client := s.generator.GetClient()
	embed, err := client.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cache[text] = embed
	s.mu.Unlock()

	return embed, nil
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return float32(dotProduct / (math.Sqrt(normA) * math.Sqrt(normB)))
}
