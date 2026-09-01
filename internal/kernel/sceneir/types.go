// Package sceneir — types.go defines the SceneIR value type and its
// supporting semantic contracts. These are the immutable envelopes that
// travel downstream from the compiler; the compiler is the only producer.
package sceneir

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

// VisualEntity is a single source-grounded visual entity extracted from a
// scene. V1 of the deterministic Rust extractor (VisualNER) produces these;
// the rule it must satisfy is NO EVIDENCE → NO ENTITY: if the entity text
// cannot be demonstrated as a substring of SourceText, it is rejected.
//
// Start/End are byte offsets into the canonical SourceText that prove the
// entity's evidence. Evidence is the verbatim excerpt at [Start, End).
type VisualEntity struct {
	// Text is the entity surface form (e.g. "feta cheese").
	Text string `json:"text"`
	// SourceTextHash binds the evidence to the exact canonical source.
	SourceTextHash string `json:"source_text_hash"`
	// Score is the deterministic visualness score in [0,1]; higher is
	// more visually concrete. NOT a provider relevance score.
	Score float32 `json:"score"`
	// Start is the inclusive byte offset into SourceText where the
	// entity's evidence begins.
	Start int `json:"start"`
	// End is the exclusive byte offset into SourceText where the
	// entity's evidence ends.
	End int `json:"end"`
	// Evidence is the verbatim SourceText excerpt at [Start, End).
	Evidence string `json:"evidence,omitempty"`
}

// SearchQuery is a single retrieval query owned by exactly one scene and
// one provider. It is the output of the QueryPlanner stage that consumes a
// SceneIR's SemanticProfile + VisualEntities. The owner fields exist so
// MediaCert can detect a query that drifted from one scene to another.
type SearchQuery struct {
	// ID is a stable query identity within the compiled scene.
	ID string `json:"id,omitempty"`
	// OwnerSegmentID is the canonical segment that owns this query; it MUST
	// match the SceneIR.SegmentID the query was planned from. A mismatch is
	// cross-scene contamination.
	OwnerSegmentID string `json:"owner_segment_id"`
	// Provider is the target provider ("artlist", "images", "youtube").
	Provider string `json:"provider"`
	// Query is the provider-facing query string.
	Query string `json:"query"`
	// EntityRef is the optional visual entity the query was derived from;
	// when set, it MUST point at a VisualEntity.Text on the owning scene.
	EntityRef string `json:"entity_ref,omitempty"`
	// SourceTextHash binds this query to the source snapshot that produced it.
	SourceTextHash string `json:"source_text_hash,omitempty"`
}

// SceneIR is the immutable intermediate representation compiled from a
// canonical VidRush segment. It carries two surfaces that downstream code
// MUST keep strictly separate:
//
//   - SourceText (+ SegmentID, Position, SourceTextHash): the immutable,
//     canonical narrative/source wording. This is what query planners
//     consume. It is copied verbatim from the input CanonicalSegment and
//     never rewritten by the LLM.
//   - NarrationText: the creative, speakable narration the LLM MAY
//     rewrite (e.g. "Get ready to dive into the vibrant world of Greek
//     cuisine..."). It must never feed query builders.
//
// SourceText != NarrationText by construction. A compiled SceneIR is a
// value type: callers receive a copy and cannot mutate the compiler's
// internal state through it.
type SceneIR struct {
	// SegmentID is the immutable canonical segment identifier. It is
	// copied verbatim from the input CanonicalSegment.ID and must never
	// be rewritten (mediterranean-01-greek-salad must NOT become scene-1).
	SegmentID string `json:"segment_id"`
	// Position is the zero-based, stable position within the canonical
	// segment list. Immutable.
	Position int `json:"position"`

	// SourceText is the immutable canonical source/narrative wording. It
	// feeds query planners. NEVER rewritten by the LLM.
	SourceText string `json:"source_text"`
	// SourceTextHash is the stable hash of SourceText, computed by the
	// canonical script.ComputeCanonicalSegmentTextHash. It lets MediaCert
	// detect that SourceText was tampered with after compilation.
	SourceTextHash string `json:"source_text_hash"`

	// NarrationText is the creative, speakable narration the LLM may
	// rewrite. It is optional: when the compiler is given no narration
	// override, it defaults to the SourceText itself. Downstream query
	// planners MUST NOT consume NarrationText.
	NarrationText string `json:"narration_text,omitempty"`

	// Profile is the single canonical per-scene semantic understanding. It is
	// ALWAYS populated by the compiler. Visual profiles are projections of
	// this value (script.BuildSegmentVisualProfile), never a second owner.
	Profile script.SegmentSemanticProfile `json:"profile"`

	// Entities are the source-grounded visual entities for this scene.
	// Populated by the deterministic VisualNER stage (Rust) after the
	// SceneIR is compiled; empty until that stage runs.
	Entities []VisualEntity `json:"entities,omitempty"`
	// VideoQueries are the video-provider (Artlist/YouTube) retrieval
	// queries owned by this scene. Populated by the QueryPlanner stage.
	VideoQueries []SearchQuery `json:"video_queries,omitempty"`
	// ImageQueries are the image-provider retrieval queries owned by
	// this scene. Populated by the QueryPlanner stage.
	ImageQueries []SearchQuery `json:"image_queries,omitempty"`
}
