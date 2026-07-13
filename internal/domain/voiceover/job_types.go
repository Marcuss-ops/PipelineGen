package voiceover

// Canonical voiceover job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeBatch is the canonical job type for batch voiceover processing.
	TypeBatch = "voiceover.batch"

	// TypeGenerate is the canonical job type for voiceover generation.
	TypeGenerate = "voiceover.generate"

	// TypeGenerateItem is the per-language child job scheduled by the
	// parent voiceover.generate handler.
	TypeGenerateItem = "voiceover.generate_item"

	// TypePromo is the canonical job type for voiceover promo processing.
	TypePromo = "voiceover.promo"
)
