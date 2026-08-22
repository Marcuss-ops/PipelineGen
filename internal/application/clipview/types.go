// Package clipview — canonical model-facing projection of one
// Qdrant-selected clip candidate. The struct is what Gemma sees;
// the omitted fields (asset_id, drive_link, folder_id tecnico,
// youtube_url, local_path, hash, filename) are what Gemma MUST
// never see.
//
// godlike/06 SSOT (one canonical owner per fact): this package is
// the SOLE owner of the CandidateView surface. Any other struct
// that carries a model-facing JSON tag for asset_id/drive_link or
// any other forbidden key is a forward-prevention violation — the
// redaction-leak guard verifies at PR time that the struct shape
// + the marshalled bytes both exclude the deny-list.
//
// Pipeline position (July 2026 user plan):
//
//	SearchSlots(plan, { Folder: ClipFolderRef(boxe) }) →
//	  per-slot Qdrant hits → ClipCandidate[] (raw, internal) →
//	  NewCandidateView(slotRef, i, Candidate) → CandidateView (Gemma-facing) →
//	  Gemma: "I pick slot-1:candidate-3 with score 0.91" →
//	  backend resolves: candidate_ref → asset_id + drive_link (private)
//
// Rationale for the field allow-list vs deny-list:
//   - Allow-list (6 fields) is what Gemma actually consumes.
//   - Deny-list (12 keys) is the inherited model-facing surface
//     ATTACK surface — an attacker that can influence Qdrant
//     payload fields tries to leak them via the model-facing
//     projection. The deny-list is the canonical guard.
//
// Diff vs NarrativeClipView (internal/domain/script/):
//   - NarrativeClipView is the slot-narrative projection consumed
//     by the script-scripter loop. It includes SlotRef and
//     excludes Score (different surface, different consumer).
//   - CandidateView is the per-Qdrant-hit projection consumed by
//     Gemma's candidate-selection phase. It includes Score and an
//     opaque per-slot Ref, and explicitly excludes SlotRef so the
//     script's internal slot taxonomy does not leak to the model.
package clipview

import "errors"

// Candidate is the raw internal hint surface for CandidateView
// projection. AssetRef + SlotRef are PRIVATE — they MUST NOT leak
// into the model-facing JSON.
//
// godlike/07 NO-FAKE-AVAILABILITY: the projection is the SOLE
// conversion from raw candidate to model-facing view. A future
// caller that constructs a CandidateView via direct struct
// literal (skipping NewCandidateView) bypasses the redaction
// guard — a future archcheck rule can pin "no CandidateView
// literals outside this package" but for now the SSOT is enforced
// via tests + the sentinel error envelope.
type Candidate struct {
	// AssetRef is the raw media_assets.id. STRIPPED on projection —
	// Gemma never sees this; the backend maps ref → asset_id
	// privately after Gemma returns the selection.
	AssetRef string

	// SlotRef is the raw slot name from ClipPrePlan (e.g. "slot-1").
	// NOT exposed in CandidateView (would leak the planner's slot
	// taxonomy); instead, it is folded into the opaque `ref` per
	// NewCandidateView's construction.
	SlotRef string

	// Description is the LLM-generated semantic description of the
	// clip. Safe to surface to the model.
	Description string

	// VisualSummary is the VLM-aggregated visual summary (one per
	// clip on asset_visual_summaries). Safe to surface.
	VisualSummary string

	// Transcript is the verbatim or windowed transcript text for
	// the clip. Safe to surface.
	Transcript string

	// DurationMs is the clip duration in milliseconds. Safe to
	// surface.
	DurationMs int64

	// Score is the cosine similarity from the Qdrant hit. Safe to
	// surface (it's the model-facing relevance signal).
	Score float64
}

// CandidateView is the canonical model-facing projection of one
// candidate. Gemma sees ONLY these 6 fields. The struct shape is
// the SOLE source of truth for "what Gemma sees" — a future field
// addition MUST land here + through the allow-list + tests, NOT
// via an ad-hoc sibling type.
//
// godlike/06 SSOT: this is the SOLE canonical model-facing
// candidate surface. Adding a JSON-tagged field that omits the
// allow-list is a forward-prevention violation caught by
// TestCandidateView_StructShapeStripsForbidden.
//
// godlike/07 NO-FAKE-AVAILABILITY: empty Ref surfaces as
// ErrCandidateViewEmptyRef (callers MUST give a non-empty opaque
// ref). JSON marshalling with omitempty does NOT silently
// backfill — the JSON object always carries the field shape per
// the struct tags above.
type CandidateView struct {
	// Ref is the opaque per-candidate identifier. Construction in
	// NewCandidateView: slotRef + ":candidate-" + index. NOT the
	// raw slot name, NOT the asset_id, NOT the content hash. The
	// backend maintains the private map ref → {asset_id, drive_link}.
	Ref string `json:"ref"`

	// Description is the LLM-generated semantic description.
	Description string `json:"description,omitempty"`

	// VisualSummary is the VLM-aggregated visual caption.
	VisualSummary string `json:"visual_summary,omitempty"`

	// Transcript is the verbatim or windowed transcript text.
	Transcript string `json:"transcript,omitempty"`

	// DurationMs is the clip duration in milliseconds.
	DurationMs int64 `json:"duration_ms,omitempty"`

	// Score is the cosine-similarity relevance signal.
	Score float64 `json:"score,omitempty"`
}

// AllowedCandidateViewJSONFields is the canonical allow-list
// (godlike/06 SSOT). The list is the single source of truth: the
// reflection guard (builder_test.go::TestCandidateView_StructShapeStripsForbidden)
// AND the JSON-marshalling guard both iterate this list. Adding
// a new JSON field to CandidateView without adding it here
// surfaces as a CI failure, not a silent introduction.
var AllowedCandidateViewJSONFields = []string{
	"ref",
	"description",
	"visual_summary",
	"transcript",
	"duration_ms",
	"score",
}

// ForbiddenCandidateViewJSONFields is the canonical deny-list
// (godlike/07 NO-FAKE-AVAILABILITY). Any of these keys appearing
// in the CandidateView JSON is a HARD redaction-leak failure —
// forward-pointer PR-REDACTION-LEAK-AUDIT can promote this to a
// CI enforcement gate across the whole repository.
//
// The list is built by enumeration rather than dynamic reflection
// because reflect alone cannot distinguish a struct field that
// was renamed-but-not-yet-removed from a deliberate leak. The
// explicit list is grep-friendly, code-review-friendly, and
// survives refactors.
var ForbiddenCandidateViewJSONFields = []string{
	// ─── Infrastructure identifiers (canonical AssetID family) ───
	"asset_id",
	"assetid",

	// ─── Drive infrastructure ───
	"drive_link",
	"drive_webviewlink",
	"drive_file_id",
	"download_link",
	"local_path",
	"relative_path",

	// ─── Folder / category side channels ───
	//
	// NOTE: `normalized_group` is the canonical routing key
	// (the Qdrant filter target) — it is NOT a forward-leak
	// target the same way `folder_id` (technical Drive folder
	// ID) is. We omit both defensively.
	"folder_id",
	"folder_path",
	"normalized_group",

	// ─── Source provenance ───
	"source",
	"source_url",
	"source_provider",
	"source_video_id",
	"youtube_url",
	"youtube_video_id",
	"channel_id",
	"channel",

	// ─── Hash / content fingerprints ───
	"hash",
	"content_hash",
	"legacy_file_md5",
	"md5",
	"md5_checksum",
	"sha256",

	// ─── Filename / display name ───
	"filename",
	"name",
	"title",
	"local_filename",

	// ─── Internal lifecycle / status (architectural not model-relevant) ───
	"lifecycle_state",
	"status",
	"job_id",
	"run_fingerprint",
	"workflow_id",
	"policy_version",

	// ─── IndexDocument / wire-only keys ───
	"qdrant_point_id",
	"index_document",

	// ─── Slot taxonomy (leaks the planner's internal naming) ───
	"slot_ref",
	"plan_id",
}

// Sentinel errors (godlike/07 fail-closed envelope contract).
var (
	// ErrCandidateViewNilReceiver: a method was invoked on a nil
	// *CandidateView pointer. Distinct from construction errors
	// so callers probe the typed envelope.
	ErrCandidateViewNilReceiver = errors.New(
		"clipview: nil receiver on CandidateView",
	)

	// ErrCandidateViewEmptyRef: the constructed CandidateView has
	// an empty Ref. ErrCandidateViewEmptyRef is the canonical
	// fail-closed surface for "the projection cannot produce a
	// model-facing ref" — callers MUST give a non-empty input so
	// the projection can never backfill a synthetic ref.
	ErrCandidateViewEmptyRef = errors.New(
		"clipview: ref is required (opaque per-candidate identifier MUST be non-empty before projection)",
	)

	// ErrCandidateViewRedactionLeak: a forbidden key appeared in
	// the marshalled JSON. THIS IS GODLIKE/07 HARD-FAIL. Use
	// errors.Is to detect at PR-time enforcement gates.
	ErrCandidateViewRedactionLeak = errors.New(
		"clipview: redaction leak detected (forbidden key in model-facing JSON)",
	)
)
