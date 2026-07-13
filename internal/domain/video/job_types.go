package video

// Canonical video/render job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeRender is the canonical job type for video rendering.
	TypeRender = "render.video"

	// TypeGenerate is the canonical job type for video generation.
	TypeGenerate = "video.generate"
)
