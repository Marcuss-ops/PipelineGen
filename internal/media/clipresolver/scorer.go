package clipresolver

import (
	"velox/go-master/internal/media/models"
)

// ApplyOntologyBoost applies boosts from ontology rules.
func ApplyOntologyBoost(scorer OntologyScorer, score float64, clip *models.MediaAsset, topic string) float64 {
	if scorer == nil {
		return score
	}
	return scorer.Apply(score, clip, topic)
}
