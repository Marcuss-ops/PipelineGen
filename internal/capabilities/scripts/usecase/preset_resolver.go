// Package scripts — preset_resolver.go applies preset semantics to
// a GenerationItemV2.
//
// As of issue 8 / Step 3 (June 2026): the canonical implementation
// lives in internal/capabilities/scripts/adapters/generation_normalizer.go
// (per AGENTS.md Pattern 8 — adapters/ is the canonical dependency
// boundary for the normalized script pipeline because it also owns
// applyConfigDefaults and applySafetyDefaults, the other two layers
// of the single precedence chain). This file is preserved as a thin
// wrapper so existing callers that imported usecase.ApplyPreset
// (e.g. tests via the `scripts` import alias) keep working; new
// callers should reference adapters.ApplyPreset directly.
//
// The canonical semantic table is in
// docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md §6
// "Required preset semantics" and covers the 5 documented presets
// (custom, with_images, full_media, catalog, search) plus
// pass-through for batch / unknown / empty Preset values.
//
// PR 8 (June 2026) precedence contract:
//
//	caller explicit > preset > config > safety
//
// PR 8 narrowing note: `with_images` no longer forces voiceover /
// document / entities / metadata. Only the canonical
// `adapters.ApplyPreset` knows the current row-by-row semantics;
// this wrapper inherits all of those guarantees by delegation.
package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ApplyPreset is a thin wrapper that delegates to the canonical
// implementation in internal/capabilities/scripts/adapters/. The
// adapter is the single source of truth for the full 5-preset
// semantics (custom / with_images / full_media / catalog / search
// + pass-through for batch / unknown / empty). This wrapper exists
// for backward compatibility with pre-Step-3 callers.
//
// Caller fields are NEVER overwritten; the wrapper inherits the
// nil-safe behavior (a nil item returns without mutation),
// idempotence (re-running with the same inputs is a no-op), and
// the caller > preset > config > safety precedence from the
// canonical implementation.
func ApplyPreset(item *scriptpkg.GenerationItemV2, preset scriptpkg.Preset) {
	adapters.ApplyPreset(item, preset)
}
