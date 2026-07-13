package youtube

// Canonical youtube job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeExtract is the canonical job type for youtube clip extraction
	// (URL -> media_assets row + outbox).
	TypeExtract = "youtube.extract"
)
