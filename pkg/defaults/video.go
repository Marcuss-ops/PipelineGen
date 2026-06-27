package defaults

// VideoConfig is the canonical configuration for script video chunking,
// effects lookup, and the JSON field name used to chain derived assets
// back to their source. It is the single source of truth for the three
// values used in the script-generation + script-history transport.
//
// HC-7 (June 2026): replaces the pre-HC-7 scattered literals:
//   - ChunkDuration: 25 (was a hard-coded default in
//     internal/platform/config/video.go::WithDefaults — line 64)
//   - EffectsDir:    "effects/" (was a magic string in stock pipeline)
//   - ParentFieldName: "parent_id" (was a string literal duplicated in
//     /api/scripts/* HTTP responses; DRIFT-23-4 was a class of bugs
//     where some responses emitted `parent_id: ""` because the literal
//     was reinvented rather than consulted)
//
// Every consumer MUST call DefaultVideoConfig() rather than hard-coding
// any of these values; the anti-reintro gate is Check 39 in
// scripts/ci-architectural-checks.sh.
type VideoConfig struct {
	// ChunkDuration is the per-chunk duration in seconds for video
	// assembly. The default (25) matches the 25-fps stock pipeline
	// cadence — a chunk of 25 frames at 25 fps ≈ 1 second on screen.
	ChunkDuration int

	// EffectsDir is the directory containing video effect assets,
	// relative to project root. Empty value disables effects.
	EffectsDir string

	// ParentFieldName is the JSON field name that /api/scripts/*
	// responses use to point a derived script back to its source.
	// MUST stay "parent_id" so reader-side code (and historical API
	// consumers) doesn't break. New readers MUST consult this constant
	// rather than hard-coding the string.
	ParentFieldName string
}

// DefaultVideoConfig returns the canonical HC-7 VideoConfig SSOT.
// Treat the returned value as immutable per consumer site (no
// process-global mutation — copy and adjust locally if needed).
//
// Shape is intentionally tiny (3 leaf fields) to keep pkg/defaults
// leaf-only: zero imports from internal/, only consumed by callers
// crossing the infra→application seam. Updates to the values MUST
// land as a new round of Check 39 + the test below.
func DefaultVideoConfig() VideoConfig {
	return VideoConfig{
		ChunkDuration:   25,
		EffectsDir:      "effects/",
		ParentFieldName: "parent_id",
	}
}
