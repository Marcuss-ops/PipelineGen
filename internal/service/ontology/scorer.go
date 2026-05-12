package ontology

import (
	"strings"

	"velox/go-master/pkg/models"
)

// Scorer implements the OntologyScorer interface.
type Scorer struct {
	registry *Registry
}

// NewScorer creates a new ontology-based scorer.
func NewScorer(registry *Registry) *Scorer {
	return &Scorer{registry: registry}
}

// Apply applies the ontology rules for a given topic to the clip score.
func (s *Scorer) Apply(score float64, clip *models.Clip, topic string) float64 {
	if s.registry == nil || topic == "" {
		return score
	}

	rule, ok := s.registry.Topics[strings.ToLower(topic)]
	if !ok {
		return score
	}

	finalScore := score

	// 1. Boost for core terms or visual synonyms
	if s.matchesAny(clip, rule.CoreTerms) || s.matchesAnySynonym(clip, rule.VisualSynonyms) {
		if rule.Boost > 0 {
			finalScore *= rule.Boost
		}
	}

	// 2. Category boost
	if s.matchesCategory(clip, rule.PreferredCategories) {
		finalScore *= 1.1 // Small bonus for category match
	}

	// 3. Avoid penalty
	if s.matchesAny(clip, rule.Avoid) {
		finalScore *= 0.5 // Significant penalty for avoid terms
	}

	return finalScore
}

func (s *Scorer) matchesAny(clip *models.Clip, terms []string) bool {
	if len(terms) == 0 {
		return false
	}

	searchText := s.getSearchText(clip)
	for _, term := range terms {
		if strings.Contains(searchText, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func (s *Scorer) matchesAnySynonym(clip *models.Clip, synonyms [][]string) bool {
	for _, group := range synonyms {
		if s.matchesAny(clip, group) {
			return true
		}
	}
	return false
}

func (s *Scorer) matchesCategory(clip *models.Clip, categories []string) bool {
	if clip.Category == "" || len(categories) == 0 {
		return false
	}
	clipCat := strings.ToLower(clip.Category)
	for _, cat := range categories {
		if clipCat == strings.ToLower(cat) {
			return true
		}
	}
	return false
}

func (s *Scorer) getSearchText(clip *models.Clip) string {
	var sb strings.Builder
	sb.WriteString(strings.ToLower(clip.Name))
	sb.WriteString(" ")
	for _, t := range clip.Tags {
		sb.WriteString(strings.ToLower(t))
		sb.WriteString(" ")
	}
	for _, st := range clip.SearchTerms {
		sb.WriteString(strings.ToLower(st))
		sb.WriteString(" ")
	}
	return sb.String()
}
