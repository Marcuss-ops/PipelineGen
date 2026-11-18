package overlays

// Package overlays — recap_candidates.go owns the Automatic Recap Builder
// (visual-only first release).
//
// A recap never rewrites approved narration: it proposes RecapCandidates
// built ONLY from already-covered chapters and bullets (Previously in this
// video…). Placement callers decide WHERE a recap is useful (after several
// chapters, before a thematic turn); this builder decides WHAT it may show.
// A spoken recap would need a new narrative segment + TTS and is out of
// scope. Deterministic: same chapters + bullets + scores → same candidates.

// RecapSource is one already-covered unit (chapter title or bullet).
type RecapSource struct {
	ID          string // chapter or bullet id (must be non-empty: provenance)
	Title       string
	SentenceIDs []int   // covered sentence ids (must be non-empty)
	Score       float64 // PhraseImpact-style importance (already computed)
}

// RecapCandidate is one visual recap card proposal (no narration).
type RecapCandidate struct {
	Title       string   `json:"title"`
	SourceIDs   []string `json:"source_ids"`
	SentenceIDs []int    `json:"sentence_ids"`
	Score       float64  `json:"score"`
}

// RecapInvalidatedBy reports whether a recap candidate depends on any
// changed source (chapter/bullet id): an edit to an earlier chapter
// invalidates the recaps built on it, never unrelated ones.
