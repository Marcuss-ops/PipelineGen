package adapters

import (
	"math"
)

// clampUnit is a neutral score-normalization helper for discovery metadata.
// It does not select or rank media candidates.
func clampUnit(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
