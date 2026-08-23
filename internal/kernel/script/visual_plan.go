// Package script — visual_plan.go defines the canonical visual plan
// surface produced by the visual planning processor.
//
// The visual plan is attached to each SpecScene and carries the
// winning layers (slot, asset_id, provider, duration) plus the
// cold candidates that were evaluated but not materialized.
package script

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"

// VisualPlan is the per-scene visual plan produced by the visual
// planning processor. It contains the ordered layers chosen for
// the scene and the list of candidates that were considered but
// kept cold.
type VisualPlan struct {
	// Layers is the ordered list of visual layers chosen for the
	// scene. Usually 1-3 layers.
	Layers []VisualLayer `json:"layers,omitempty"`

	// Candidates holds the non-winning candidates that were
	// evaluated for this scene. They remain cold (not materialized).
	Candidates []VisualCandidate `json:"candidates,omitempty"`
}

// VisualLayer is one resolved visual layer for a scene.
type VisualLayer struct {
	// Slot is the canonical slot kind (primary_video, secondary_image,
	// evidence_overlay, ...).
	Slot media.SlotKind `json:"slot,omitempty"`

	// AssetID is the canonical ID of the selected asset. After
	// materialization this is the local media_assets.id.
	AssetID string `json:"asset_id,omitempty"`

	// Provider is the canonical source tag (drive, artlist, pexels,
	// youtube, local, ...).
	Provider string `json:"provider,omitempty"`

	// StartMs is the optional layer start offset in milliseconds.
	StartMs int64 `json:"start_ms,omitempty"`

	// EndMs is the optional layer end offset in milliseconds.
	EndMs int64 `json:"end_ms,omitempty"`

	// DurationMs is the layer duration in milliseconds.
	DurationMs int64 `json:"duration_ms,omitempty"`

	// Score is the final ranker score for the winning candidate.
	Score float64 `json:"score,omitempty"`
}

// VisualCandidate is a cold (not materialized) candidate that the
// visual planning processor evaluated but did not select.
type VisualCandidate struct {
	// AssetID is the candidate's asset identifier.
	AssetID string `json:"asset_id,omitempty"`

	// Provider is the candidate's source tag.
	Provider string `json:"provider,omitempty"`

	// Score is the candidate's ranker score.
	Score float64 `json:"score,omitempty"`

	// DurationMs is the candidate's duration in milliseconds.
	DurationMs int64 `json:"duration_ms,omitempty"`

	// MediaType is the candidate's media type (video/image/audio).
	MediaType string `json:"media_type,omitempty"`

	// RightsStatus is the candidate's rights status.
	RightsStatus string `json:"rights_status,omitempty"`
}
