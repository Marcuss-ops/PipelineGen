// Package defaults provides coalesce-style helper functions.
//
// Deprecated: use pkg/defaults instead. This file delegates to the canonical
// implementation in pkg/defaults/ so call sites can migrate incrementally.
package platform

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"
)

// Deprecated: use pkg/defaults.String.
func String(val, fallback string) string { return defaults.String(val, fallback) }

// Deprecated: use pkg/defaults.Int.
func Int(val, fallback int) int { return defaults.Int(val, fallback) }

// Deprecated: use pkg/defaults.Float64.
func Float64(val, fallback float64) float64 { return defaults.Float64(val, fallback) }
