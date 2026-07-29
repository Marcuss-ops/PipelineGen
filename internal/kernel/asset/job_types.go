package asset

// Canonical asset job type constants.
// Per godlike/02 capability-specific constants live in their owning domain package.
const (
	// TypeResolve is the canonical job type for semantic asset resolution.
	TypeResolve = "assets.resolve"

	// TypeTextMaterialize is the canonical job type for the text-track
	// materialization pipeline.
	TypeTextMaterialize = "asset.text.materialize"
)
