package clipresolver

import (
	"math"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
	}{
		{
			name:     "identical vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "orthogonal vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{0, 1, 0},
			expected: 0.0,
		},
		{
			name:     "opposite vectors",
			a:        []float64{1, 0, 0},
			b:        []float64{-1, 0, 0},
			expected: -1.0,
		},
		{
			name:     "different lengths",
			a:        []float64{1, 0},
			b:        []float64{1, 0, 0},
			expected: 0.0,
		},
		{
			name:     "zero vector",
			a:        []float64{0, 0, 0},
			b:        []float64{1, 0, 0},
			expected: 0.0,
		},
		{
			name:     "typical similarity",
			a:        []float64{1, 1, 0},
			b:        []float64{1, 0, 0},
			expected: 1.0 / math.Sqrt(2.0), // dot=1, normA=sqrt(2), normB=1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.expected) > 1e-9 {
				t.Errorf("CosineSimilarity() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCalculateVectorScore(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{1, 0, 0}
	got := CalculateVectorScore(a, b)
	if got != 1.0 {
		t.Errorf("CalculateVectorScore() = %v, want 1.0", got)
	}
}
