package ontology

import (
	"testing"

	"velox/go-master/pkg/models"
)

func TestScorer_Apply(t *testing.T) {
	reg := &Registry{
		Topics: map[string]TopicRule{
			"amish": {
				CoreTerms:      []string{"amish"},
				VisualSynonyms: [][]string{{"buggy", "horse wagon"}},
				Avoid:          []string{"computer", "tech"},
				Boost:          1.3,
			},
		},
	}
	s := NewScorer(reg)

	tests := []struct {
		name     string
		clip     *models.MediaAsset
		topic    string
		base     float64
		expected float64
	}{
		{
			name:     "boost by core term",
			clip:     &models.MediaAsset{Name: "Amish family walking"},
			topic:    "amish",
			base:     10.0,
			expected: 13.0,
		},
		{
			name:     "boost by visual synonym",
			clip:     &models.MediaAsset{Name: "Horse buggy on road"},
			topic:    "amish",
			base:     10.0,
			expected: 13.0,
		},
		{
			name:     "penalty for avoid term",
			clip:     &models.MediaAsset{Name: "Amish using computer"},
			topic:    "amish",
			base:     10.0,
			expected: 6.5, // 10 * 1.3 (boost) * 0.5 (penalty) = 6.5
		},
		{
			name:     "no match for topic",
			clip:     &models.MediaAsset{Name: "Modern city"},
			topic:    "amish",
			base:     10.0,
			expected: 10.0,
		},
		{
			name:     "unknown topic",
			clip:     &models.MediaAsset{Name: "Amish family"},
			topic:    "unknown",
			base:     10.0,
			expected: 10.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.Apply(tt.base, tt.clip, tt.topic)
			if got != tt.expected {
				t.Errorf("Apply() = %v, want %v", got, tt.expected)
			}
		})
	}
}
