package ports

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// MediaSamplerPort is the single selection boundary for media candidates.
// Providers may discover candidates, but only this port may choose a winner.
type MediaSamplerPort interface {
	Sample(ctx context.Context, sceneID, subject string, terms []string, candidates []scriptpkg.SegmentAssetCandidate, allowReuse bool) (string, error)
}

// AssetBinding is one entry in the Go composition's authoritative
// already-assigned ledger: an asset bound to its owning scene. It is the
// wire-agnostic form of the Rust mediasampler's BoundEntry for the
// select_group operation.
type AssetBinding struct {
	AssetID string `json:"asset_id"`
	SceneID string `json:"scene_id"`
}

// MediaSamplerGroupPort is the optional MultiImage 2.0 selection boundary:
// instead of the single `select` winner it returns 2–5 distinct ranked
// winners for one scene, in the worker's rank order. The returned slice has
// at most groupSize entries: when fewer candidates pass the hard
// constraints the worker returns fewer members and the caller adapts the
// layout (x4 keeps working when the fifth image is missing) instead of
// padding with duplicates. boundAssets carries the caller's
// already-assigned ledger; entries with an owner other than sceneID are
// refused by the worker as already_bound, so a group never repeats an asset
// from an earlier scene.
type MediaSamplerGroupPort interface {
	SampleGroup(ctx context.Context, sceneID, subject string, terms []string, candidates []scriptpkg.SegmentAssetCandidate, allowReuse bool, groupSize int, bound []AssetBinding) ([]scriptpkg.SegmentAssetCandidate, error)
}
