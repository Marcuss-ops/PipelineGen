// Package usecase — ports_pre_planner.go declares the canonical
// ClipPrePlanner port and the input/output shape it consumes and
// produces. The Pre-Planner converts operator intent (title, topic,
// source_text, segments, tone, target duration, max clips) into a
// deterministic, provenance-attached ClipPrePlan: an ordered list
// of ClipSearchSlot records that the downstream SlotSearchPort and
// the shared ClipSampler consume to produce the final
// ResolvedClipPlan.
//
// godlike/06 SSOT (data/config ownership): the planner owns the
// shape transition from operator intent to visual requirements.
// godlike/07 NO-FAKE-AVAILABILITY: the planner is a pure function;
// if the planner cannot produce a valid plan for the given request,
// it returns an error rather than producing a degraded no-op plan.
//
// Stage plan:
//   - FASE 1: types declared here (port-local). This commit.
//   - FASE 2: types migrate to internal/domain/script (no shape
//     change; relocation only) when the domain package adopts the
//     planner vocabulary.
//   - FASE 3: deterministic planner implementation ships.
//   - FASE 4+: SlotSearchPort.SearchSlots extension, shared
//     ClipSampler, and backend Ref -> clip_id binding depend on
//     this port and ship in their own commits.
package usecase

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── Input ───────────────────────────────────────────────────────────────

// PlanRequest is the canonical operator intent fed to the
// ClipPrePlanner. Every field maps to a stable piece of caller
// intent; the planner reads each and emits a ClipPrePlan.
//
// Determinism: identical PlanRequest yields a byte-identical
// ClipPrePlan. The planner does NOT perform I/O; the sameness
// guarantee is the audit-able contract the rest of the pipeline
// relies on.
type PlanRequest struct {
	// Title is the canonical document/video title. Used as a
	// planner input when the source_text is sparse; carried
	// verbatim into the planner-derived ClipPrePlan.Title.
	Title string

	// Topic is the canonical narrative topic. The planner
	// uses it to score primary-slot visual intent (e.g.
	// "Pacquiao vs Broner recap"). Required: an empty Topic
	// surfaces a planner error rather than a degenerate
	// plan.
	Topic string

	// SourceText is the IMMUTABLE provenance anchor. The
	// planner computes SourceHash = sha256(SourceText) and
	// stores it on ClipPrePlan.SourceHash and on every
	// SourceAnchor.SourceHash. The planner NEVER rewrites
	// or paraphrases the text: excerpted spans are byte
	// ranges of the original, not rewordings.
	SourceText string

	// Segments is the optional ordered narrative blocks.
	// When non-empty: one Required slot per non-empty
	// Segment (1:1 mapping). When empty: the planner
	// derives slot count from source_text length, capped
	// by MaxClips.
	Segments []scriptpkg.ScriptSegment

	// Tone is the editorial tone (informative, dramatic,
	// neutral, etc.). Carried as informational; future VLM
	// conditioning may consume it.
	Tone string

	// TargetDurationMs is the desired total runtime of the
	// produced video. The planner sizes each slot so the
	// sum of slot.TargetDurationMs is within ±10% of this
	// envelope.
	TargetDurationMs int64

	// MaxClips is the hard ceiling on slot count. The
	// planner never emits more than MaxClips slots. When
	// <= 0 the planner chooses (currently 8 by default;
	// subject to FASE 3 follow-up).
	MaxClips int
}

// ── Output ──────────────────────────────────────────────────────────────

// ClipPrePlan is the deterministic, provenance-attached output of
// the Pre-Planner. It is the audit-able contract for the rest of
// the pipeline: SlotSearchPort reads Slots and finds candidates;
// the Sampler reads candidates + Slots and emits one clip per
// slot.
type ClipPrePlan struct {
	// Version is the contract version (currently 1). Bumped
	// on any breaking shape change; conservative on additive
	// changes.
	Version int

	// SourceHash is the sha256 of the original SourceText at
	// planning time. Every ClipSearchSlot.SourceAnchor
	// carries the same value. The backend (FASE 5) rejects
	// any later binding whose anchor hash no longer matches.
	SourceHash string

	// Title is the planner-derived cinematic title. May
	// differ cosmetically from req.Title; always non-empty
	// on a valid plan.
	Title string

	// Slots is the ordered list of visual requirements. The
	// order matches the operator's narrative order
	// (Segments[] order when present, source_text order
	// otherwise). Slot.Refs are "slot-1".."slot-N" with N
	// = len(Slots) and never re-order or re-index between
	// planner runs of the same input.
	Slots []ClipSearchSlot
}

// ClipSearchSlot is a single visual requirement emitted by the
// Pre-Planner. The SlotSearchPort finds candidates for it; the
// shared ClipSampler picks one per slot.
type ClipSearchSlot struct {
	// Ref is the temporary skeleton key the rest of the
	// pipeline threads (the model-facing prompt sees it;
	// the backend resolves Ref -> clip_id at binding
	// time). Format: "slot-N" with N = 1..len(plan.Slots).
	// NOT a clip_id; the model must never see one and must
	// refuse to report anything other than {ref, text}.
	Ref string

	// Topic is the per-slot narrative topic. Verbatim from
	// the corresponding Segment.Topic when Segments[] is
	// non-empty; otherwise a topic-derived slice of
	// source_text at the anchor offset. The editor-facing
	// prompt displays this verbatim.
	Topic string

	// SourceAnchor is the immutable reference to the
	// SourceText span this slot represents. The planner
	// NEVER edits SourceText; the slot's chosen clip MUST
	// depict a moment that overlaps this span.
	SourceAnchor SourceAnchor

	// SearchQuery is the per-slot query sent to the
	// SlotSearchPort. Composition: Title + Topic + visual
	// narrative verbs. Stable: the same PlanRequest
	// produces the same SearchQuery text (byte-identical).
	SearchQuery string

	// VisualIntent describes what the chosen clip is
	// expected to show. Used at index time by the VLM
	// cross-check (FASE VLM) and surfaced in the model-
	// facing prompt as the slot's narrative view header.
	// Stable: deterministic string.
	VisualIntent string

	// TargetDurationMs is the desired runtime for the
	// chosen clip. The sampler enforces a soft floor
	// (>= 0.8 * TargetDurationMs) and a hard ceiling
	// (<= 2 * TargetDurationMs).
	TargetDurationMs int64

	// Required marks the slot as must-have. A sampler that
	// cannot satisfy all Required slots fails the planner's
	// overall result (no partial plan).
	Required bool
}

// SourceAnchor is an immutable reference to a SourceText span.
// Offsets are byte offsets into SourceText committed at planning
// time. The planner sets SourceHash; downstream code uses it to
// enforce source-text immutability.
type SourceAnchor struct {
	// SourceHash is the sha256 of the SourceText this
	// anchor was computed against. Set by the planner
	// (= plan.SourceHash); downstream rejects mismatches.
	SourceHash string

	// StartOffset is the inclusive byte offset in the
	// original SourceText.
	StartOffset int

	// EndOffset is the exclusive byte offset. EndOffset >
	// StartOffset always; EndOffset <= len(SourceText)
	// always.
	EndOffset int

	// Excerpt is a pre-extracted prose slice (<= 500 runes
	// by convention) for the planner's own use AND the
	// model-facing prompt. Verbatim from SourceText; never
	// reworded.
	Excerpt string
}

// ── Port ─────────────────────────────────────────────────────────────────

// ClipPrePlanner is the canonical port for the Clip Pre-Planner.
// The Planner converts operator intent into a deterministic
// ClipPrePlan without any I/O: it only reads inputs and emits
// slots. SlotSearchPort and ClipSampler handle the downstream
// retrieval and selection.
//
// Lifecycle: the Planner is invoked exactly once per SourceCurate
// job, BEFORE the SlotSearchPort produces candidates. The
// resulting ClipPrePlan is the audit-able provenance of every
// clip the Sampler chooses.
type ClipPrePlanner interface {
	// Plan computes a ClipPrePlan from the operator intent.
	// Implementation rules:
	//   - Pure function: identical PlanRequest yields a
	//     byte-identical ClipPrePlan (including Ref indices,
	//     SourceAnchor offsets, SearchQuery strings).
	//   - SourceText immutable: SourceHash =
	//     sha256(req.SourceText); every SourceAnchor.Source
	//     Hash equals plan.SourceHash.
	//   - Refs stable: "slot-1" is always the FIRST slot.
	//     PlanRequest with k Segments produces slots
	//     "slot-1" .. "slot-k" in segment order.
	//   - Slot count <= req.MaxClips when req.MaxClips > 0.
	//   - Sum of slot.TargetDurationMs within +/- 10% of
	//     req.TargetDurationMs when TargetDurationMs > 0.
	//   - Every non-empty Segment yields at least one
	//     Required slot when Segments is non-empty.
	//   - Empty Topic returns an error; empty SourceText
	//     yields a degenerate (single) Required slot
	//     referencing offset 0..0 (the planner does not
	//     invent text).
	//   - On success returns a non-nil plan; zero plans are
	//     errors.
	Plan(ctx context.Context, req PlanRequest) (*ClipPrePlan, error)

	// ValidatePlan is the post-construction guardrail. The
	// Planner impl calls it BEFORE returning so the
	// invariants above are guaranteed by construction;
	// callers may also call it on a plan received over the
	// wire (e.g. resume / replay) to refuse plans that
	// fail the contract.
	ValidatePlan(plan *ClipPrePlan) error
}
