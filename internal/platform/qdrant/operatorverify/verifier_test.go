package operatorverify

import (
	"testing"
)

func TestVectorDimensions(t *testing.T) {
	cases := []struct {
		name     string
		vectors  map[string]any
		expected int
	}{
		{
			name:     "empty map",
			vectors:  nil,
			expected: 0,
		},
		{
			name: "float32 vector",
			vectors: map[string]any{
				"default": []float32{1.0, 2.0, 3.0},
			},
			expected: 3,
		},
		{
			name: "float64 vector",
			vectors: map[string]any{
				"default": []float64{1.0, 2.0, 3.0, 4.0},
			},
			expected: 4,
		},
		{
			name: "generic []any vector from JSON decoder",
			vectors: map[string]any{
				"default": []any{1.0, 2.0},
			},
			expected: 2,
		},
		{
			name: "sparse vector is ignored, dense vector is used",
			vectors: map[string]any{
				"sparse":  map[string]any{"indices": []int{0, 1}, "values": []float32{1.0, 1.0}},
				"default": []float32{1.0, 2.0, 3.0},
			},
			expected: 3,
		},
		{
			name: "only sparse vectors returns zero",
			vectors: map[string]any{
				"sparse": map[string]any{"indices": []int{0, 1}, "values": []float32{1.0, 1.0}},
			},
			expected: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := vectorDimensions(tc.vectors)
			if got != tc.expected {
				t.Fatalf("vectorDimensions(%v) = %d, want %d", tc.vectors, got, tc.expected)
			}
		})
	}
}

func TestVectorDimensionsDeterministic(t *testing.T) {
	vectors := map[string]any{
		"z_vec": []float32{1.0, 2.0},
		"a_vec": []float32{1.0, 2.0, 3.0, 4.0, 5.0},
	}
	got := vectorDimensions(vectors)
	if got != 5 {
		t.Fatalf("expected deterministic selection of a_vec (5 dims), got %d", got)
	}
}
