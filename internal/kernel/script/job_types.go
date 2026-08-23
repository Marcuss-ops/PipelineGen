package script

// Canonical script job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeGenerate is the canonical job type for script generation.
	TypeGenerate = "script.generate"

	// TypeVoiceoverSibling is the sibling job type for voiceover assets
	// spawned by the script generation handler.
	TypeVoiceoverSibling = "script.spawn_voiceover"

	// TypeImageSibling is the sibling job type for image assets spawned
	// by the script generation handler.
	TypeImageSibling = "script.spawn_images"

	// TypeGenerateItem is the per-item child job type for script.generate batches.
	TypeGenerateItem = "script.generate_item"
)
