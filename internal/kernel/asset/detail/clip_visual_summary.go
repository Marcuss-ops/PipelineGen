// Package asset - clip_visual_summary.go defines the canonical domain
// types for the VLM-generated visual summary of a media asset.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE canonical owner of the VisualSummary row shape for the
// asset_visual_summaries table. All other layers (infrastructure
// SQLite repo, Qdrant payload mapper, outbox/event-key derivation)
// read/write this struct - they MUST NOT construct an alternative
// mirror of these fields.
//
// godlike/07 NO-FAKE-AVAILABILITY: the VisualSummary row is NOT a
// cached representation of something the sampler can re-derive. The
// Qdrant payload carries visual_summary/visible_actions/visible_entities
// ONLY when a real VLM pass ran (the row's preprocessing_version +
// model_version prove it). A pre-VLM-era clip with NO row emits NO
// payload keys (omitempty contract - see qdrant.payload_builder_test.go).
//
// Reconstructability: the visual_summary/visible_actions/visible_entities
// payload is rebuildable from SQLite by re-running the VLM pass on the
// same frames + same model version - i.e. ReindexVerifier is the
// canonical post-reindex consistency gate. See
// internal/platform/qdrant/verification/verifier.go for the
// verifier extension that cross-checks the SQLite row vs the
// Qdrant payload.
package detail

import (
	"errors"
	"time"
)

// VisualSummary is the canonical domain model for one row in
// asset_visual_summaries. There is at most ONE row per media asset
// (PRIMARY KEY = asset_id) - the visual summary is a single
// aggregate over the frames sampled from the clip's local_path.
//
// Field roles:
//
//   - AssetID                       -> canonical media_assets.id; the table's
//     PRIMARY KEY. Format varies by source
//
//   - VisualSummaryText             -> aggregated caption from the VLM
//     pass over the sampled frames. Empty
//     when no frames were sampled or the
//     VLM returned an empty description.
//
//   - VisibleActions                -> union of action verbs the VLM
//     identified across all sampled frames
//     (e.g. "throws_punch", "defends_ring").
//     Nil/empty when no actions detected.
//
//   - VisibleEntities               -> union of named entities the VLM
//     identified across all sampled frames
//     (e.g. "boxer_1", "ring"). Nil/empty
//     when no entities detected.
//
//   - FrameCount                    -> number of frames the sampler
//     actually evaluated (1 frame every
//     N seconds -> floor(duration_sec / N)
//     expected).
//
//   - IntervalSeconds               -> the N parameter used for this
//     pass. Stored so a reindex at a
//     different interval is auditable
//     (godlike/07: provenance must survive).
//
//   - PreprocessingVersion          -> canonical "vlm-sampler/<semver>"
//     identifier of the frame-sampler +
//     FFmpeg pipeline that produced this
//     row. Used by the Qdrant payload's
//     visual_preprocessing_version field.
//
//   - ModelName                     -> VLM model identifier (e.g. "llava-1.6-7b").
//     Empty when the VLM pass did not run
//     (NO_ROW case).
//
//   - ModelVersion                  -> VLM checkpoint version (e.g.
//     "2026-07-13"). The dual identifier
//     ModelName+ModelVersion is intentionally
//     redundant with the PreprocessingVersion
//     tuple so an operator can answer
//     "which VLM was used?" without parsing
//     the preprocessing identifier.
//
//   - SourceHash                    -> SHA-256 of
//     (sorted(visible_actions) ||
//     sorted(visible_entities) ||
//     model_name || model_version ||
//     preprocessing_version || frame_count).
//     Used by the supersede gate - a re-run
//     that produces the same SourceHash is a
//     no-op; a different SourceHash forces
//     a Qdrant re-index even when the
//     content_hash is unchanged - e.g.
//     a model_version bump.
//
//   - SampledAt                     -> wall-clock time the VLM pass
//     completed. Empty when the row was
//     NOT produced by a real pass (legacy
//     / pre-VLM-era migration).
//
//   - CreatedAt / UpdatedAt         -> row lifecycle timestamps
//     (godlike/06 SSOT - same convention
//     as asset_text_tracks).
//
// Empty-marked fields: VisualSummaryText "" means "no caption";
// VisibleActions nil means "no actions"; VisibleEntities nil means
// "no entities". The Qdrant payload omits all three (omitempty)
// in this case - see qdrant.payload_builder_test.go for the
// strict-emit test contract that pins this behaviour.
type VisualSummary struct {
	AssetID              string    `json:"asset_id"`
	VisualSummaryText    string    `json:"visual_summary,omitempty"`
	VisibleActions       []string  `json:"visible_actions,omitempty"`
	VisibleEntities      []string  `json:"visible_entities,omitempty"`
	FrameCount           int       `json:"frame_count,omitempty"`
	IntervalSeconds      float64   `json:"interval_seconds,omitempty"`
	PreprocessingVersion string    `json:"preprocessing_version,omitempty"`
	ModelName            string    `json:"model_name,omitempty"`
	ModelVersion         string    `json:"model_version,omitempty"`
	SourceHash           string    `json:"source_hash,omitempty"`
	SampledAt            string    `json:"sampled_at,omitempty"`
	CreatedAt            time.Time `json:"created_at,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitempty"`
}

// ----- Limits (compile-time audit-stable) --------------------------
//
// godlike/06 SSOT: thresholds live in ONE place. Operators tune
// them by editing this file. No environment-variable injection for
// now; FASE-9 may promote these to per-row config if needed.

const (
	// MaxVisualSummaryChars is the audit-stable cap on the visual
	// summary caption length. The Python sidecar enforces the same
	// limit at the conversion layer (truncate + warn). 512 chars
	// matches the qdrant.payload_mapper search-field convention.
	MaxVisualSummaryChars = 512

	// MaxVisibleItems is the audit-stable cap on VisibleActions
	// and VisibleEntities array sizes. 32 items per array matches
	// the qdrant.payload_mapper expected-bounded-shape contract.
	MaxVisibleItems = 32
)

// Typed validation errors. Each is exposed as a sentinel so
// callers (the SQLite repository, the Qdrant payload mapper,
// the admin reindex command) can programmatic-distinguish via
// errors.Is - the canonical godlike/07 NO-FAKE-AVAILABILITY
// surface. Validate() joins all field-level violations into a
// single error chain via errors.Join (Go 1.20+) so callers can
// recover the full reason set without parsing the error string.
var (
	// ErrVisualSummaryEmptyAssetID: caller passed an empty
	// asset_id. asset_visual_summaries.asset_id is PRIMARY KEY
	// NOT NULL - an empty ID would violate the schema constraint.
	ErrVisualSummaryEmptyAssetID = errors.New("visual_summary: asset_id must not be empty")

	// ErrVisualSummarySummaryTooLong: VisualSummaryText exceeds
	// MaxVisualSummaryChars. Caller truncates before persistence
	// to surface a typed error rather than silent truncation.
	ErrVisualSummarySummaryTooLong = errors.New("visual_summary: visual_summary text exceeds MaxVisualSummaryChars")

	// ErrVisualSummaryActionsTooLong: VisibleActions exceeds
	// MaxVisibleItems. Distinct from the entities check so callers
	// can branch on which array overflowed.
	ErrVisualSummaryActionsTooLong = errors.New("visual_summary: visible_actions exceeds MaxVisibleItems")

	// ErrVisualSummaryEntitiesTooLong: VisibleEntities exceeds
	// MaxVisibleItems.
	ErrVisualSummaryEntitiesTooLong = errors.New("visual_summary: visible_entities exceeds MaxVisibleItems")

	// ErrVisualSummaryNegativeFrameCount: FrameCount < 0. The
	// canonical writer always sets FrameCount >= 0; the SQLite
	// schema CHECK constraint also enforces it.
	ErrVisualSummaryNegativeFrameCount = errors.New("visual_summary: frame_count must be >= 0")

	// ErrVisualSummaryNegativeInterval: IntervalSeconds < 0.
	// Allowed sentinel value is 0 (the EmptyAssetDB legacy
	// surface); the canonical writer always sets IntervalSeconds
	// > 0 once a real VLM pass has run.
	ErrVisualSummaryNegativeInterval = errors.New("visual_summary: interval_seconds must be >= 0")

	// ErrVisualSummaryNilReceiver: Validate() called on a nil
	// *VisualSummary pointer. Distinct from the empty-asset-id
	// case so callers can branch on "receiver itself is nil" vs
	// "receiver carries a zero-value / missing field".
	ErrVisualSummaryNilReceiver = errors.New("visual_summary: nil receiver")
)

// EmptyAssetDB constructs the canonical no-op row used by the
// legacy / pre-VLM migration path. Returns a Validate-clean
// VisualSummary with asset_id only, no VLM pass invoked.
//
// godlike/07 NO-FAKE-AVAILABILITY: the empty row is NOT a
// "we tried" placeholder. The Qdrant payload omits
// visual_summary/visible_actions/visible_entities entirely
// when the row is empty. See qdrant.payload_builder_test.go for
// the omitempty regression contract.
