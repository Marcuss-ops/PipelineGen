// Package scriptgeneration — model_values.go: the primitive and reference value
// types of the pure domain model (ZERO I/O, ZERO external dependencies).
//
// Language, Source, ClipReference (+ its canonical duration resolution),
// AudioReference — the shape the aggregates in model.go
// embed. The render/audio projections live in model_render.go.
//
// Extracted 2026-09-12 from model.go to keep every file under
// max_lines_per_file_strict=600 (godlike/08 forward-prevention cap).
package scriptgeneration

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Value types ─────────────────────────────────────────────────────

const (
	SourceText     = scriptpkg.SourceText
	SourceClips    = scriptpkg.SourceClips
	SourceCatalog  = scriptpkg.SourceCatalog
	SourceSearch   = scriptpkg.SourceSearch
	SourceCurate   = scriptpkg.SourceCurate
	SourceResearch = scriptpkg.SourceResearch
)

// ── Request clone helpers (merged from builder.go) ──
//
// Merged here to keep builder.go under max_lines_per_file_strict=600:
// pure zero-I/O request normalization helpers. No behaviour change.

func toLanguages(src []string) []contract.Language {
	if src == nil {
		return nil
	}
	dst := make([]contract.Language, 0, len(src))
	for _, lang := range src {
		if lang != "" {
			dst = append(dst, contract.Language(lang))
		}
	}
	return dst
}

// copyStrings returns a copy of the string slice (nil-safe).
func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

func cloneFloat64(src *float64) *float64 {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func cloneInt(src *int) *int {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func cloneAnimationCounts(src map[string]int) map[string]int {
	dst := make(map[string]int, len(src)+1)
	for key, value := range src {
		dst[key] = value
	}
	// Production default: fuse same-scene overlays when the empty interval
	// between them is at most five seconds. Explicit chain_disabled /
	// chain_gap_ms / chain_enabled always win; the latter enables chaining
	// with the same five-second default used by the empty configuration.
	if dst["chain_disabled"] <= 0 && dst["chain_gap_ms"] <= 0 {
		dst["chain_gap_ms"] = 5000
	}
	return dst
}
