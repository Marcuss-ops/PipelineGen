package media

// Canonical version strings for the resolution pipeline. These values
// are the single source of truth for tracing, cache fingerprints, and
// decision audit metadata. New components that need a version MUST
// add a constant here rather than hardcoding a "*-v1" literal elsewhere.
const (
	VersionBrain            = "brain-v1"
	VersionIntentRegistry   = "intent-registry-v1"
	VersionEmbedding        = "multilingual-e5-v1"
	VersionDiversityPolicy  = "diversity-policy-v1"
	VersionSlotSampler      = "slot-sampler-v1"
	VersionProviderRegistry = "provider-registry-v1"
	VersionMediaRanker      = "media-ranker-v2"
	VersionScenePlanner     = "scene-planner-v1"
	VersionVisualIntent     = "visual-intent-v1"
)
