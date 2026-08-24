package images

// Canonical image job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeImagesGenerate is the canonical job type for AI image generation.
	TypeImagesGenerate = "images.generate"

	// TypeGenerateGoogle is the canonical job type for Google Slides image
	// generation.
	TypeGenerateGoogle = "image.generate.google"
)
