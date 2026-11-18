// Package overlays — preset_motion_pools.go: the certified, read-only motion
// pool accessors for the phrase/text lanes.
//
// Split out of preset_motion_selection.go to respect max_lines_per_file_strict
// (same package, no behavior change).
package overlays

// CertifiedPhraseMotions returns the certified render-safe motion pool this
// build rotates over. It is the membership authority a caller-supplied pool is
// validated against (see PlanInput.PhraseMotions): every id is a catalog motion
// whose text, semantic-highlight span, animator and companion-layer lowering
// is implemented by RenderingGen. Typewriter stays retired; the new documentary
// highlighting family is phrase-relative and part of the certified pool.
//
// The returned slice is a copy — callers may keep it without pinning the
// package's own storage.
func CertifiedPhraseMotions() []string {
	return append([]string(nil), phraseMotionCandidates...)
}
