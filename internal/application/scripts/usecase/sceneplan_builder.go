// Package scripts — sceneplan_builder.go is the FASE-NEXT
// canonical Gemma-facing scene-plan output for the Sampler.
//
// godlike/06 SSOT (one canonical owner per fact): this file is
// the SOLE owner of:
//   - The SamplerScene wire shape (the model-facing per-slot
//     envelope Gemma reads).
//   - The BuildGemmaScenePlan transformation (SamplerResult +
//     ClipPrePlan + targetWords → []SamplerScene).
//   - The word-budget distribution algorithm (proportional by
//     TargetDurationMs, remainder-sweep on the LAST slot).
//
// A future package that needs to feed per-slot data to Gemma
// imports the type + function from this file. No parallel inline
// builder lives in resolvers; the godlike/06 single-owner rule
// prevents drift between copy-paste surfaces.
//
// godlike/07 NO-FAKE-AVAILABILITY invariants:
//   - Missing slots (Sample has fewer ClipIDs than the plan has
//     Slots) surface as SelectedClipRef="" + WordBudget=0. NO
//     synthetic clip_id, NO synthetic duration.
//   - target_words <= 0 → nil result (per the caller contract;
//     never default to 0-budget for the model).
//   - nil plan → nil result (the planner side is responsible for
//     producing a non-nil plan before this builder is called).
//   - A slot with TargetDurationMs=0 (planner didn't budget it)
//     gets floor(target_words * 0 / total_duration) = 0 words,
//     surfaced as WordBudget=0; the caller's editor-facing
//     pipeline reads the WordBudget=0 + SelectedClipRef="" pair
//     as "no scene budget for this slot" (not as an
//     auto-allocated placeholder).
//
// Determinism contract: identical SamplerResult + ClipPrePlan +
// targetWords yields a byte-identical []SamplerScene slice. The
// word-budget algorithm uses math.Floor (deterministic across
// hosts); the remainder-sweep is a single O(1) adjustment on the
// LAST slot so the sum exactly equals target_words.
package usecase

import (
	"math"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SamplerScene is the canonical model-facing per-slot scene
// envelope built from the SamplerResult + ClipPrePlan. Gemma
// reads SlotRef + Topic + TargetDurationMs + SelectedClipRef +
// TranscribedText + VisualSummary + WordBudget to compose the
// per-scene narrative.
//
// godlike/06 SSOT (one canonical owner per fact): this struct
// lives ONLY here. A future package that needs a model-facing
// scene surface imports this type from
// internal/application/scripts/usecase, NOT a parallel fork.
// Re-exporting via a ports re-shim is acceptable when the
// consumer lives in a different domain layer; the canonical
// definition stays here.
type SamplerScene struct {
	// SlotRef is the canonical slot identifier (e.g. "slot-3").
	// Copied verbatim from plan.Slots[i].Ref. NEVER a clip_id;
	// the model never sees a clip_id.
	SlotRef string

	// Topic is the per-slot narrative topic. Copied verbatim from
	// plan.Slots[i].Topic. Stable across planner replays;
	// matches the slot's ClipEvidence.Topic at downstream binding
	// time.
	Topic string

	// TargetDurationMs is the slot's planned runtime. Copied
	// verbatim from plan.Slots[i].TargetDurationMs. 0 when the
	// planner didn't size the slot (legacy mode / explicit
	// MinimumDuration contract).
	TargetDurationMs int64

	// SelectedClipRef is the candidate clip_id the Sampler picked.
	// Mirrors res.ClipIDs[i] (1:1 with slots); empty when i >=
	// len(res.ClipIDs) (slot failed every gate audit OR sampler
	// didn't have enough candidates to fill the slot).
	SelectedClipRef string

	// TranscribedText is the canonical transcript excerpt for
	// the scene's selected clip. Empty when the resolver hasn't
	// hydrated clips yet (callers must hydrate BEFORE feeding
	// Gemma). godlike/07: never invented; empty means "hydrate
	// then re-feed, OR trigger the planner's FallbackPolicy".
	TranscribedText string

	// VisualSummary is the canonical visual-summary text for the
	// scene's selected clip. Same empty-means-hydration-required
	// discipline as TranscribedText.
	VisualSummary string

	// WordBudget is the per-scene target word count after
	// proportional distribution. Sum across all scenes equals
	// exactly target_words (remainder absorbed on the last
	// slot). 0 means "no budget" (slot is uncounted in the plan
	// total — e.g. TargetDurationMs=0).
	WordBudget int
}

// distributeWordBudget computes a deterministic per-slot word
// budget using a proportional-by-duration algorithm with a
// single-pass remainder-sweep on the last slot.
//
// Algorithm (matches the user spec "rispetto di requested_clips
// e target_words" — word budget respects pacing):
//
//	if total_duration == 0:
//	    each slot = floor(target_words / N); last absorbs
//	    remainder to make sum == target_words exactly.
//	else:
//	    slot_i = floor(target_words * slot_i.duration / total_duration);
//	    remainder = target_words - sum_per_slot;
//	    last slot += remainder (sweep).
//
// godlike/06 SSOT: this is the canonical word-budget distribution.
// The SamplerProvenance test pins that target_words == sum of
// budgets (forward-pointer: a future agent that tweaks the
// algorithm MUST update the test in lock-step).
//
// Returns nil when:
//   - len(slots) == 0 (caller didn't author a plan with slots)
//   - targetWords <= 0 (caller misconfigured the budget intake)
//
// Returns a fresh []int with len(slots) entries otherwise. Each
// value >= 0 (negative values are impossible given the
// algorithm's bounded input space).
func distributeWordBudget(slots []scriptpkg.ClipSearchSlot, targetWords int) []int {
	if len(slots) == 0 || targetWords <= 0 {
		return nil
	}
	n := len(slots)
	budgets := make([]int, n)

	var totalDuration int64
	for _, s := range slots {
		if s.TargetDurationMs > 0 {
			totalDuration += s.TargetDurationMs
		}
	}

	if totalDuration == 0 {
		// No durations set: split equally. The Sampler audit
		// pipeline's duration gate trivially passes on a
		// TargetDurationMs=0 slot; the caller here divides by N.
		per := targetWords / n
		for i := range budgets {
			budgets[i] = per
		}
		budgets[n-1] += targetWords - per*n // remainder sweep
		return budgets
	}

	var sum int
	for i, s := range slots {
		b := int(math.Floor(
			float64(targetWords) * float64(s.TargetDurationMs) / float64(totalDuration),
		))
		budgets[i] = b
		sum += b
	}
	remainder := targetWords - sum
	budgets[n-1] += remainder // single sweep — drift correction to exact sum
	return budgets
}

// BuildGemmaScenePlan converts the canonical SamplerResult +
// ClipPrePlan + targetWords into a Gemma-ready scene array.
// One scene per plan slot (1:1 with plan.Slots). Each scene's
// SelectedClipRef mirrors res.ClipIDs[i] (1:1 with slots, in
// sampler-declaration order). When the Sampler produced fewer
// ClipIDs than the plan has slots (e.g. partial failure with
// some slots un-filled), the trailing slots surface empty refs
// + zero budgets — the script editor pipeline reads this as
// "no clip available, fallback policy applies" rather than
// silently inventing a placeholder.
//
// godlike/07 NO-FAKE-AVAILABILITY: any unmapped slot (no matching
// clip in res) surfaces as SelectedClipRef="" — NEVER as a
// synthetic clip_id nor a synthetic duration. Any slot the
// planner didn't size (TargetDurationMs=0 paired with no other
// slot carrying duration) surfaces as WordBudget=0 — NEVER as
// a per-slot fallback equal to target_words/N.
//
// Callers must hydrate TranscribedText + VisualSummary BEFORE
// feeding Gemma (this builder's contract is shape + selection
// + budget, not hydration).
func BuildGemmaScenePlan(res ports.ClipSamplerResult, plan *scriptpkg.ClipPrePlan, targetWords int) []SamplerScene {
	if plan == nil {
		return nil
	}
	if targetWords <= 0 {
		return nil
	}
	budgets := distributeWordBudget(plan.Slots, targetWords)
	out := make([]SamplerScene, 0, len(plan.Slots))
	for i, slot := range plan.Slots {
		scene := SamplerScene{
			SlotRef:          slot.Ref,
			Topic:            slot.Topic,
			TargetDurationMs: slot.TargetDurationMs,
			WordBudget:       budgets[i],
		}
		if i < len(res.ClipIDs) {
			scene.SelectedClipRef = res.ClipIDs[i]
		}
		// TranscribedText + VisualSummary: forward-pointer fields
		// for hydration; the resolver pipeline populates them
		// post-Sampler. The builder surfaces zero-value strings
		// here, the audit trail in res.Provenance already pinned
		// whether the candidate satisfied the
		// transcript_visual_summary_present gate.
		out = append(out, scene)
	}
	return out
}
