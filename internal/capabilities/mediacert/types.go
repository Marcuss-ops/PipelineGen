// Package mediacert — types.go defines the certification input/output value
// types: the Spec (what the run must satisfy), the MediaResult (what the run
// actually produced) and the Report (per-check PASS/FAIL + CERTIFIED flag).
package mediacert

import (
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SpecSegment is the per-segment semantic contract a run must satisfy.
// required_concepts are the source-grounded anchors that MUST appear among
// the segment's visual terms/entities (e.g. "feta", "tomatoes", "olives"
// for the greek-salad segment). A segment missing a required concept fails
// ENTITY GROUNDING / SEMANTIC PROFILES.
type SpecSegment struct {
	// ID is the canonical segment_id the run must preserve verbatim.
	ID string `json:"id"`
	// Subject is the expected single canonical subject of the segment
	// (e.g. "greek salad"). The winner asset's subject must match this.
	Subject string `json:"subject"`
	// RequiredConcepts are the source-grounded anchors that must appear
	// among the segment's visual terms or entities.
	RequiredConcepts []string `json:"required_concepts"`
	// WinnerSubjectMatch is the subject the winner asset's inferred
	// subject must be compatible with. When empty, the spec's Subject is
	// used. Used by the ARTLIST RELEVANCE check to reject a boxing clip
	// bound to a Greek Salad segment.
	WinnerSubjectMatch string `json:"winner_subject_match,omitempty"`
}

// Spec is the top-level certification specification. It declares the
// structural counts and the per-segment semantic contract. The fixture
// tests/fixtures/vidrush/mediterranean_top5_expected.json is the canonical
// Spec instance.
type Spec struct {
	// Segments is the expected number of segments in the result.
	Segments int `json:"segments"`
	// EntitiesPerSegment is the expected number of entities per segment.
	EntitiesPerSegment int `json:"entities_per_segment"`
	// ImagesPerSegment is the expected number of images per segment.
	ImagesPerSegment int `json:"images_per_segment"`
	// VideoProvider is the only video provider the run may use.
	VideoProvider string `json:"video_provider"`
	// AllowCrossSceneAssetReuse controls the CROSS-SCENE REUSE check.
	// When false, the same asset may not be bound to two segments.
	AllowCrossSceneAssetReuse bool `json:"allow_cross_scene_asset_reuse"`
	// SegmentsExpected is the per-segment semantic contract, in order.
	SegmentsExpected []SpecSegment `json:"segments_expected"`
}

// ResultSegment is the per-segment slice of a MediaResult. It is a thin
// projection of script.VidRushSegmentResult + the SceneIR that produced it,
// exposing only the fields the certifier checks. The projection keeps
// mediacert decoupled from the full wire shape of VidRushSegmentResult so
// the certifier can be unit-tested in isolation with a synthetic result.
type ResultSegment struct {
	// SegmentID is the canonical segment_id the run carried downstream.
	SegmentID string `json:"segment_id"`
	// Position is the zero-based position within the run.
	Position int `json:"position"`
	// SourceText is the immutable source text the segment carried. It is
	// compared against the SceneIR's SourceText to detect tampering.
	SourceText string `json:"source_text"`
	// SourceTextHash is the stamped hash of SourceText.
	SourceTextHash string `json:"source_text_hash"`
	// EntityEvidence records per-entity source spans in the certified result.
	// When present, it must contain one entry per extracted entity.
	EntityEvidence []sceneir.VisualEntity `json:"entity_evidence,omitempty"`
	// SourceIdentity is the compiler snapshot used to detect downstream
	// rewrites of canonical identity fields.
	Identity *sceneir.SourceIdentity `json:"identity,omitempty"`
	// NarrationText is the creative narration the LLM produced. It is
	// allowed to diverge from SourceText.
	NarrationText string `json:"narration_text,omitempty"`
	// SceneIR is the compiled SceneIR for the segment. When present, the
	// certifier uses its immutable identity + profile; when absent, the
	// certifier falls back to the flat fields above + the Insights.
	SceneIR *sceneir.SceneIR `json:"scene_ir,omitempty"`
	// VideoQueries and ImageQueries carry explicit query ownership from the
	// runtime planner. Legacy Insights strings remain supported for replay.
	VideoQueries []sceneir.SearchQuery `json:"video_queries,omitempty"`
	ImageQueries []sceneir.SearchQuery `json:"image_queries,omitempty"`
	// Insights is the canonical per-segment semantic/asset insights
	// surface produced by VidRush (entities, visual profile, queries).
	Insights script.SegmentInsights `json:"insights"`
	// Assets is the selected asset bundle for the segment.
	Assets script.SegmentAssetSelection `json:"assets"`
}

// MediaResult is the full run result submitted for certification. A run
// with status=SUCCEEDED but a semantically wrong MediaResult must be
// rejected: that is the whole point of MediaCert.
type MediaResult struct {
	// JobStatus is the run's lifecycle status (e.g. "SUCCEEDED"). The
	// certifier does NOT trust it: a SUCCEEDED run with a boxing clip for
	// Greek Salad must still return CERTIFIED=false.
	JobStatus string `json:"job_status"`
	// Segments is the per-segment result, in canonical position order.
	Segments []ResultSegment `json:"segments"`
}

// CheckName is the typed name of a certification check.
type CheckName string

const (
	CheckSceneIdentity      CheckName = "SCENE IDENTITY"
	CheckSourceImmutability CheckName = "SOURCE IMMUTABILITY"
	CheckSemanticProfiles   CheckName = "SEMANTIC PROFILES"
	CheckArtlistRelevance   CheckName = "ARTLIST RELEVANCE"
	CheckEntityGrounding    CheckName = "ENTITY GROUNDING"
	CheckImageFanout        CheckName = "IMAGE FANOUT"
	CheckQueryOwnership     CheckName = "QUERY OWNERSHIP"
	CheckAssetOwnership     CheckName = "ASSET OWNERSHIP"
	CheckCrossSceneReuse    CheckName = "CROSS-SCENE REUSE"
	CheckProviderPolicy     CheckName = "PROVIDER POLICY"
	CheckCrossContamination CheckName = "CROSS CONTAMINATION"
)

// CheckResult is the outcome of one certification check.
type CheckResult struct {
	Name   CheckName `json:"name"`
	Passed bool      `json:"passed"`
	// PassCount/TotalCount render the X/Y metric (e.g. 5/5). When the
	// check is a simple boolean, TotalCount=1 and PassCount=1 on pass.
	PassCount  int `json:"pass_count,omitempty"`
	TotalCount int `json:"total_count,omitempty"`
	// Violations lists the per-segment failures that caused a non-pass.
	Violations []Violation `json:"violations,omitempty"`
}

// Violation is one semantic failure surfaced by a check.
type Violation struct {
	SegmentID string `json:"segment_id,omitempty"`
	Rule      string `json:"rule"`
	Detail    string `json:"detail"`
}

// Report is the certification verdict. CERTIFIED is true ONLY when every
// check passed; a single failed check makes CERTIFIED false even when the
// run's JobStatus was SUCCEEDED. This is the explicit rejection of the
// count-only test that declared success at a semantically broken pipeline.
type Report struct {
	JobStatus string        `json:"job_status"`
	Certified bool          `json:"certified"`
	Checks    []CheckResult `json:"checks"`
}
