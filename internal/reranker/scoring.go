// Package reranker — scoring utilities for CrossEncoder reranker integration.
package reranker

// NormalizeScores normalizes a batch of reranker scores to [0, 1] range.
// CrossEncoder scores are not always normalized; this ensures they're
// comparable with Qdrant scores (which are cosine similarities in [0, 1]).
func NormalizeScores(scores map[string]float64) map[string]float64 {
	if len(scores) == 0 {
		return scores
	}

	minScore, maxScore := scores[firstKey(scores)], scores[firstKey(scores)]
	for _, score := range scores {
		if score < minScore {
			minScore = score
		}
		if score > maxScore {
			maxScore = score
		}
	}

	out := make(map[string]float64, len(scores))

	// If all scores are identical, return 0.5 for all
	if maxScore == minScore {
		for id := range scores {
			out[id] = 0.5
		}
		return out
	}

	for id, score := range scores {
		out[id] = (score - minScore) / (maxScore - minScore)
	}

	return out
}

// MixedScore computes the final score by blending Qdrant (bi-encoder) and
// reranker (cross-encoder) scores with a configurable weight.
//
// Formula: final = qdrantScore * (1 - weight) + rerankScore * weight
//
// Default weight of 0.35 means: 65% Qdrant similarity + 35% reranker precision.
func MixedScore(qdrantScore, rerankScore, weight float64) float64 {
	if weight < 0 {
		weight = 0
	}
	if weight > 1 {
		weight = 1
	}
	return qdrantScore*(1-weight) + rerankScore*weight
}

func firstKey(m map[string]float64) string {
	for k := range m {
		return k
	}
	return ""
}
