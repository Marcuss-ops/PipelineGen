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
// Stage plan (RESOLVED at FASE-2, July 2026):
//   - FASE 1: types declared here (port-local). Shipped.
//   - FASE 2: type aliases for the canonical scriptpkg.* shapes
//     land HERE; this port no longer carries local-port struct
//     shadows for ClipPrePlan / ClipSearchSlot / SourceAnchor.
//     godlike/06 SSOT (one canonical owner per fact): the
//     internal/kernel/script package owns the wire shape; the
//     port re-exports them by alias so the planner's return
//     types are the canonical struct by construction. No shape
//     change. No data-loss. PlanRequest stays local because the
//     domain does not (yet) own the operator-intent shape.
//   - FASE 3: deterministic planner implementation ships.
//   - FASE 4+: SlotSearchPort.SearchSlots extension, shared
//     ClipSampler, and backend Ref -> clip_id binding depend on
//     this port and ship in their own commits.
//
// Compile-time identity pin: see
// pacquiao_broner_pre_planner_schema_contract_test.go for the
// `var _ ClipPrePlan = scriptpkg.ClipPrePlan{}` lines that prove
// the alias identity at `go test` build time. Re-introducing a
// local-port struct shadow here will break those pins.
package usecase

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	// ItemID is the canonical generation item identifier the
	// caller uses to track this plan's lineage. The planner
	// echoes it on every SourcePlanningError so operator
	// dashboards can correlate failures without inspecting
	// the request body. Empty ItemID is allowed (the
	// planner just leaves it blank on errors); callers
	// should populate it for audit traceability per the
	// godlike/06 SSOT contract.
	ItemID string

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

// ClipPrePlan is the TYPE ALIAS for the canonical
// scriptpkg.ClipPrePlan (FASE-2, July 2026). godlike/06 SSOT:
// the planner's return type IS the canonical domain struct.
// Adding `Fingerprint` (or any other field) to the canonical
// type adds it here by construction; removing it removes it
// here. The wire shape used for cache invalidation,
// SlotSearchPort consumption, and downstream binder is the
// scriptpkg shape — nothing local. See source_spec.go for the
// canonical doc and the Validate() method that catches plan-
// level drift (slot refs, SourceHash, Title).
type ClipPrePlan = scriptpkg.ClipPrePlan

// ClipSearchSlot is the TYPE ALIAS for the canonical
// scriptpkg.ClipSearchSlot (FASE-2, July 2026). godlike/06 SSOT:
// the per-slot shape the SlotSearchPort reads IS the canonical
// domain struct. Required is a bare `json:"required"` (no
// omitempty) so a `false` value survives JSON round-trip;
// silence would conflate 'explicit optional' with 'schema
// missing' — see source_spec.go for the canonical contract.
// See pacquiao_broner_pre_planner_schema_contract_test.go for the
// compile-time pin that proves this alias identity.
type ClipSearchSlot = scriptpkg.ClipSearchSlot

// SourceAnchor is the TYPE ALIAS for the canonical
// scriptpkg.SourceAnchor (FASE-2, July 2026). godlike/06 SSOT:
// the per-anchor byte-range identity IS the canonical domain
// struct. The planner emits offsets into the canonicalized
// text (never raw user bytes); SourceHash is the parent
// ClipPrePlan.SourceHash and provides the anti-drift gate the
// backend relies on. See source_spec.go for the canonical
// Validate(parentPlanSourceHash) method.
type SourceAnchor = scriptpkg.SourceAnchor

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
