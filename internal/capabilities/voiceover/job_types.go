package voiceover

// Canonical voiceover job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeGenerate is the canonical job type for voiceover generation
	// (script + lang -> TTS audio).
	TypeGenerate = "voiceover.generate"

	// ── Commit 9.1 (PR-KERNEL-JOB-POPULATE follow-up, July 2026) ────
	// The following constants are required by the back-compat
	// alias layer in internal/domain/job/job.go (re-added by
	// PipelineGen Bot during the Commit 9 type-rename race).
	// PR-KERNEL-JOB-POPULATE step 1 (commit 9.1) restores them
	// so the domain/job aliases resolve.

	// TypeBatch is the canonical job type for batch voiceover
	// generation (multiple TTS items in one job).
	TypeBatch = "voiceover.batch"

	// TypeGenerateItem is the canonical job type for a single
	// voiceover-generation item (per-line TTS dispatch).
	TypeGenerateItem = "voiceover.generate_item"

	// TypePromo is the canonical job type for voiceover promo
	// generation (operator-curated promotional clip TTS).
	TypePromo = "voiceover.promo"
)
