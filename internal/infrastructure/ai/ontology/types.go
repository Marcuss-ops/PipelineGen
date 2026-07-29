package ontology

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

// TopicRule defines the scoring rules for a specific topic.
type TopicRule struct {
	CoreTerms           []string   `yaml:"core_terms"`
	VisualSynonyms      [][]string `yaml:"visual_synonyms"`
	Avoid               []string   `yaml:"avoid"`
	PreferredCategories []string   `yaml:"preferred_categories"`
	Boost               float64    `yaml:"boost"`
}

// Registry holds the mapping of topics to their scoring rules.
type Registry struct {
	Topics map[string]TopicRule
}

// OntologyScorer defines the interface for applying ontology-based scoring.
type OntologyScorer interface {
	Apply(score float64, clip *asset.Asset, topic string) float64
}
