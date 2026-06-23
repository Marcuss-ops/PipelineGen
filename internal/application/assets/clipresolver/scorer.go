package clipresolver

import (
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ApplyOntologyBoost applies boosts from ontology rules.
func ApplyOntologyBoost(scorer OntologyScorer, score float64, clip *asset.Asset, topic string) float64 {
	if scorer == nil {
		return score
	}
	return scorer.Apply(score, clip, topic)
}
