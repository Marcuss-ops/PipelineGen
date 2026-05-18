package clipresolver

import (
	"math"
	"velox/go-master/internal/media/models"
)

// CalculateVectorScore calculates the vector similarity score between a clip and a query embedding.
func CalculateVectorScore(clipEmbedding, queryEmbedding []float64) float64 {
	return CosineSimilarity(clipEmbedding, queryEmbedding)
}

// CosineSimilarity calculates the cosine similarity between two vectors.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ApplyOntologyBoost applies boosts from ontology rules.
func ApplyOntologyBoost(scorer OntologyScorer, score float64, clip *models.MediaAsset, topic string) float64 {
	if scorer == nil {
		return score
	}
	return scorer.Apply(score, clip, topic)
}
