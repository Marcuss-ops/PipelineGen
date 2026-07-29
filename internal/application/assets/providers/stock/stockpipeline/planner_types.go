package stockpipeline

const (
	explicitClipAutoSegmentThresholdSec = 60
	explicitClipAutoSegmentSeconds      = 5
)

// Canonical SourceProvider bucket identifiers (PR-003, July 2026).
// godlike/06 SSOT — one canonical owner per fact: the inference
// helper inferSourceProvider lives in planner_source.go (the canonical
// place where SourceID is consumed). All downstream consumers
// (ChunkState, ChunkMetadataEntry, StepRunner publishers) reference
// these constants via plan.SourceProvider — no stringly-typed
// branch anywhere in the pipeline.
const (
	SourceProviderYouTube = "youtube"
	SourceProviderPexels  = "pexels"
	SourceProviderPixabay = "pixabay"
	// SourceProviderUnknown is the godlike/07 NO-FAKE-AVAILABILITY
	// sentinel for non-classifiable URLs (direct mp4 blobs, Vimeo,
	// archive.org, etc.). Inference emits this literal — empty
	// string is reserved for "not yet inferred" (defensive only;
	// build paths always fill this field on a populated plan).
	SourceProviderUnknown = "unknown"
)

// ClipPlan, ClipPlanner, ErrPlannerBudgetTooSmall,
// NewDeterministicPlanner, ErrExplicitPlannerNoClips,
// NewExplicitPlanner are canonical in types/ — see aliases.go.
