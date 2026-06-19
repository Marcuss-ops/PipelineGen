// Deprecated: use pkg/sliceutil instead.
// This file delegates to the canonical implementation in pkg/sliceutil/.
package platform

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// Deprecated: use pkg/sliceutil.UniqueStrings.
func UniqueStrings(input []string) []string { return sliceutil.UniqueStrings(input) }

// Deprecated: use pkg/sliceutil.UniqueStringsCI.
func UniqueStringsCI(input []string) []string { return sliceutil.UniqueStringsCI(input) }

// Deprecated: use pkg/sliceutil.MinInt.
func MinInt(a, b int) int { return sliceutil.MinInt(a, b) }

// Deprecated: use pkg/sliceutil.TrimStrings.
func TrimStrings(items []string) []string { return sliceutil.TrimStrings(items) }

// Deprecated: use pkg/sliceutil.Clamp.
func Clamp(v, lo, hi int) int { return sliceutil.Clamp(v, lo, hi) }

// Deprecated: use pkg/sliceutil.ClampFloat64.
func ClampFloat64(v, lo, hi float64) float64 { return sliceutil.ClampFloat64(v, lo, hi) }

// Deprecated: use pkg/sliceutil.AppendUniqueStrings.
func AppendUniqueStrings(dst []string, values ...string) []string {
	return sliceutil.AppendUniqueStrings(dst, values...)
}

// Deprecated: use pkg/sliceutil.UniqueLimitedStrings.
func UniqueLimitedStrings(values []string, limit int) []string {
	return sliceutil.UniqueLimitedStrings(values, limit)
}

// Deprecated: use pkg/sliceutil.GroupSentences.
func GroupSentences(sentences []string, size int) []string { return sliceutil.GroupSentences(sentences, size) }
