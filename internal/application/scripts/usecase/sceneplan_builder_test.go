// Package scripts — sceneplan_builder_test.go pins the FASE-NEXT
// BuildGemmaScenePlan contract:
//
//  1. Happy path: one scene per plan slot, mirroring SlotRef +
//     Topic + TargetDurationMs verbatim; SelectedClipRef mirrors
//     res.ClipIDs[i] 1:1.
//  2. Word budget: proportional-by-duration with remainder-sweep
//     on the LAST slot; sum(x_budgets) == target_words exactly.
//  3. Nil-safety: nil plan / zero target → nil result.
//  4. Forward-pointer hydration discipline: empty
//     TranscribedText/VisualSummary on output (hydrated by the
//     resolver path post-Sampler, NOT here).
//  5. No-synthetic fail-closed: missing slots (len(res.ClipIDs) <
//     len(plan.Slots)) surface SelectedClipRef="" — NOT a
//     synthetic clip_id nor a synthetic duration.
package usecase

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Fixture helpers ─────────────────────────────────────────────────

// samplePlan returns a 3-slot ClipPrePlan with deterministic
// durations (6000 / 12000 / 6000 = total 24000 ms). The slot ref
// + topic strings are stable so the test surface is reproducible.
func samplePlan() *scriptpkg.ClipPrePlan {
	return &scriptpkg.ClipPrePlan{
		Version:    1,
		SourceHash: "src-hash-sample",
		Title:      "Pacquiao vs Broner recap",
		Slots: []scriptpkg.ClipSearchSlot{
			{Ref: "slot-1", Topic: "Pacquiao opening jab", TargetDurationMs: 6000},
			{Ref: "slot-2", Topic: "Broner mid-round stance", TargetDurationMs: 12000},
			{Ref: "slot-3", Topic: "Pacquiao closing combo", TargetDurationMs: 6000},
		},
	}
}

// sampleRes returns a SamplerResult that mirrors samplePlan 1:1
// (len(ClipIDs)==len(plan.Slots)).
func sampleRes() ports.ClipSamplerResult {
	return ports.ClipSamplerResult{
		ClipIDs:     []string{"clip-a", "clip-b", "clip-c"},
		SearchItems: nil,                           // not consumed by the builder at this stage
		Provenance:  scriptpkg.SamplerProvenance{}, // not consumed by the builder
	}
}

// TestBuildGemmaScenePlan_HappyPath passes a 3-slot plan + 3-clip
// result + 1000-word target and asserts every scene's fields mirror
// the inputs verbatim + WordBudget follows the proportional
// algorithm + sum(budgets)==target.
func TestBuildGemmaScenePlan_HappyPath(t *testing.T) {
	plan := samplePlan()
	res := sampleRes()
	got := BuildGemmaScenePlan(res, plan, 1000)
	if len(got) != 3 {
		t.Fatalf("len(scenes) = %d, want 3", len(got))
	}
	planSlots := plan.Slots
	resClipIDs := res.ClipIDs
	for i, scene := range got {
		if scene.SlotRef != planSlots[i].Ref {
			t.Errorf("scene[%d].SlotRef = %q, want %q", i, scene.SlotRef, planSlots[i].Ref)
		}
		if scene.Topic != planSlots[i].Topic {
			t.Errorf("scene[%d].Topic = %q, want %q", i, scene.Topic, planSlots[i].Topic)
		}
		if scene.TargetDurationMs != planSlots[i].TargetDurationMs {
			t.Errorf("scene[%d].TargetDurationMs = %d, want %d", i, scene.TargetDurationMs, planSlots[i].TargetDurationMs)
		}
		if scene.SelectedClipRef != resClipIDs[i] {
			t.Errorf("scene[%d].SelectedClipRef = %q, want %q (mirrors res.ClipIDs[i] 1:1)", i, scene.SelectedClipRef, resClipIDs[i])
		}
		if scene.TranscribedText != "" || scene.VisualSummary != "" {
			t.Errorf("scene[%d] hydration fields must be empty (forward-pointer: hydration is post-Sampler); got Transcribed=%q VisualSummary=%q",
				i, scene.TranscribedText, scene.VisualSummary)
		}
	}
	// Sum(budgets) == target_words exactly (remainder-sweep on last slot).
	var sum int
	for _, s := range got {
		sum += s.WordBudget
	}
	if sum != 1000 {
		t.Errorf("sum(WordBudget) = %d, want 1000 (remainder-sweep invariant violated)", sum)
	}
	// Distribution sanity: slot-2 (duration=12000ms = 50% of total 24000)
	// should get the largest budget (>= slot-1/3 results).
	if got[1].WordBudget < got[0].WordBudget {
		t.Errorf("slot-2 (longer duration) should have a larger budget than slot-1: got %d vs %d",
			got[1].WordBudget, got[0].WordBudget)
	}
}

// TestBuildGemmaScenePlan_NilPlan_ReturnsNil pins the godlike/07
// nil-safety discipline: nil plan = nil result (no panic, no
// synthetic default).
func TestBuildGemmaScenePlan_NilPlan_ReturnsNil(t *testing.T) {
	if got := BuildGemmaScenePlan(ports.ClipSamplerResult{}, nil, 1000); got != nil {
		t.Errorf("nil plan must return nil result (no synthetic default); got %v", got)
	}
}

// TestBuildGemmaScenePlan_ZeroTargetWords_ReturnsNil pins the
// caller-misconfig envelope: targetWords <= 0 means the budget
// intake is broken; surface nil rather than silently defaulting
// to budget=1 per slot.
func TestBuildGemmaScenePlan_ZeroTargetWords_ReturnsNil(t *testing.T) {
	plan := samplePlan()
	if got := BuildGemmaScenePlan(sampleRes(), plan, 0); got != nil {
		t.Errorf("targetWords=0 must return nil (no default budget); got %v", got)
	}
	if got := BuildGemmaScenePlan(sampleRes(), plan, -100); got != nil {
		t.Errorf("targetWords<0 must return nil; got %v", got)
	}
}

// TestBuildGemmaScenePlan_FewerResThanSlots_NoSynthetic pins the
// godlike/07 NO-FAKE-AVAILABILITY invariant at the scene-plan
// output: when the sampler produces fewer clips than the plan
// has slots, the trailing slots surface SelectedClipRef="" — NOT
// a synthetic clip_id or auto-clamped duration.
func TestBuildGemmaScenePlan_FewerResThanSlots_NoSynthetic(t *testing.T) {
	plan := samplePlan()
	res := ports.ClipSamplerResult{
		ClipIDs: []string{"clip-a"}, // only 1 of 3 plan slots filled
	}
	got := BuildGemmaScenePlan(res, plan, 1000)
	if len(got) != 3 {
		t.Fatalf("len(scenes) = %d, want 3 (1:1 with plan.Slots even when res is short)", len(got))
	}
	if got[0].SelectedClipRef != "clip-a" {
		t.Errorf("scene[0].SelectedClipRef = %q, want clip-a", got[0].SelectedClipRef)
	}
	if got[1].SelectedClipRef != "" {
		t.Errorf("scene[1].SelectedClipRef = %q, want empty (no synthetic clip_id)", got[1].SelectedClipRef)
	}
	if got[2].SelectedClipRef != "" {
		t.Errorf("scene[2].SelectedClipRef = %q, want empty (no synthetic clip_id)", got[2].SelectedClipRef)
	}
}

// TestBuildGemmaScenePlan_HydrationForwardPointer pins that the
// builder surfaces zero-value hydration fields (NOT hardcore
// hydration logic): the resolvers hydrate downstream; the
// builder's job is shape + selection + budget. A future
// hydration helper will fill TranscribedText + VisualSummary;
// the builder's contract is "leave them empty until hydrated".
func TestBuildGemmaScenePlan_HydrationForwardPointer(t *testing.T) {
	plan := samplePlan()
	res := sampleRes()
	got := BuildGemmaScenePlan(res, plan, 1000)
	for i, s := range got {
		if s.TranscribedText != "" {
			t.Errorf("scene[%d].TranscribedText should be empty (forward-pointer hydration); got %q",
				i, s.TranscribedText)
		}
		if s.VisualSummary != "" {
			t.Errorf("scene[%d].VisualSummary should be empty (forward-pointer hydration); got %q",
				i, s.VisualSummary)
		}
	}
}

// ── distributeWordBudget tests ─────────────────────────────────────

// TestDistributeWordBudget_ProportionalByDuration pins the
// canonical ratio: a slot with duration=50% of total gets
// floor(50% * target). Slot-2 of the sample plan (12000/24000 =
// 50%) of an 800-word target should get floor(0.5*800)=400 words.
func TestDistributeWordBudget_ProportionalByDuration(t *testing.T) {
	plan := samplePlan()
	budgets := distributeWordBudget(plan.Slots, 800)
	if len(budgets) != 3 {
		t.Fatalf("len(budgets) = %d, want 3", len(budgets))
	}
	// slot-1: 6000/24000 = 25% → floor(0.25*800) = 200
	if budgets[0] != 200 {
		t.Errorf("budgets[0] = %d, want 200 (slot-1 duration 25%% of total)", budgets[0])
	}
	// slot-2: 12000/24000 = 50% → floor(0.5*800) = 400
	if budgets[1] != 400 {
		t.Errorf("budgets[1] = %d, want 400 (slot-2 duration 50%% of total)", budgets[1])
	}
	// slot-3: 6000/24000 = 25% → floor(0.25*800) = 200
	if budgets[2] != 200 {
		t.Errorf("budgets[2] = %d, want 200 (slot-3 duration 25%% of total == slot-1)", budgets[2])
	}
	// Exact sum (no drift):
	var sum int
	for _, b := range budgets {
		sum += b
	}
	if sum != 800 {
		t.Errorf("sum(budgets) = %d, want 800 (exact sum required; remainder-sweep invariant)", sum)
	}
}

// TestDistributeWordBudget_RemainderSweepOnLastSlot pins the
// godlike/06 determinism contract: when math.Floor drops a
// remainder (sum < target_words), the LAST slot absorbs the
// drift so sum == target exactly.
// Setup: 3 slots with durations 10000 / 10000 / 10000 (total
// 30000) and target_words=100 → each gets floor(1/3 * 100)=33.
// Sum = 99. Remainder = 1. Last slot absorbs → 34. Total = 100.
func TestDistributeWordBudget_RemainderSweepOnLastSlot(t *testing.T) {
	slots := []scriptpkg.ClipSearchSlot{
		{Ref: "slot-1", Topic: "alpha", TargetDurationMs: 10000},
		{Ref: "slot-2", Topic: "beta", TargetDurationMs: 10000},
		{Ref: "slot-3", Topic: "gamma", TargetDurationMs: 10000},
	}
	budgets := distributeWordBudget(slots, 100)
	if len(budgets) != 3 {
		t.Fatalf("len(budgets) = %d, want 3", len(budgets))
	}
	if budgets[0] != 33 || budgets[1] != 33 {
		t.Errorf("expected budgets[0]=33, budgets[1]=33 (math.Floor of 1/3*100); got %d, %d",
			budgets[0], budgets[1])
	}
	if budgets[2] != 34 {
		t.Errorf("budgets[2] = %d, want 34 (last slot absorbs remainder; sum invariant)", budgets[2])
	}
	var sum int
	for _, b := range budgets {
		sum += b
	}
	if sum != 100 {
		t.Errorf("sum(budgets) = %d, want 100 (exact; remainder-sweep must hit target)", sum)
	}
}

// TestDistributeWordBudget_NoDurationsSet_EqualDistribution pins
// the fallback: when no slot has TargetDurationMs > 0, the
// distribution splits equally with the LAST slot absorbing any
// remainder.
func TestDistributeWordBudget_NoDurationsSet_EqualDistribution(t *testing.T) {
	slots := []scriptpkg.ClipSearchSlot{
		{Ref: "slot-1", Topic: "alpha"}, // no duration
		{Ref: "slot-2", Topic: "beta"},  // no duration
		{Ref: "slot-3", Topic: "gamma"}, // no duration
	}
	budgets := distributeWordBudget(slots, 100)
	if len(budgets) != 3 {
		t.Fatalf("len(budgets) = %d, want 3", len(budgets))
	}
	// 100 / 3 = 33 remainder 1. Slot 0=33, slot 1=33, slot 2 = 33+1 = 34.
	if budgets[0] != 33 || budgets[1] != 33 {
		t.Errorf("expected budgets[0]=33 budgets[1]=33; got %d, %d", budgets[0], budgets[1])
	}
	if budgets[2] != 34 {
		t.Errorf("budgets[2] = %d, want 34 (last slot absorbed the remainder)", budgets[2])
	}
	var sum int
	for _, b := range budgets {
		sum += b
	}
	if sum != 100 {
		t.Errorf("sum(budgets) = %d, want 100 (exact sum required regardless of path)", sum)
	}
}

// TestDistributeWordBudget_NilSlots_ReturnsNil pins the
// nil-safety: empty slot list → nil budgets (NOT a single
// zero-budget slot — the planner-side error surfaces elsewhere).
func TestDistributeWordBudget_NilSlots_ReturnsNil(t *testing.T) {
	if got := distributeWordBudget(nil, 100); got != nil {
		t.Errorf("nil slots must return nil budgets; got %v", got)
	}
	if got := distributeWordBudget([]scriptpkg.ClipSearchSlot{}, 100); got != nil {
		t.Errorf("empty slots must return nil budgets; got %v", got)
	}
}

// TestDistributeWordBudget_ZeroTarget_ReturnsNil pins the
// zero-budget intake: targetWords <= 0 → nil (NOT auto-default
// to budget=1 per slot).
func TestDistributeWordBudget_ZeroTarget_ReturnsNil(t *testing.T) {
	slots := samplePlan().Slots
	if got := distributeWordBudget(slots, 0); got != nil {
		t.Errorf("targetWords=0 must return nil; got %v", got)
	}
	if got := distributeWordBudget(slots, -10); got != nil {
		t.Errorf("targetWords<0 must return nil; got %v", got)
	}
}

// TestBuildGemmaScenePlan_PreservesPlanOrder pins ordering: the
// scene array preserves plan.Slots order verbatim (no sort, no
// re-order). Critical for narrative-order downstream consumers.
func TestBuildGemmaScenePlan_PreservesPlanOrder(t *testing.T) {
	plan := &scriptpkg.ClipPrePlan{
		Version:    1,
		SourceHash: "src-hash-order",
		Title:      "ordered slots",
		Slots: []scriptpkg.ClipSearchSlot{
			{Ref: "slot-3", Topic: "third", TargetDurationMs: 1000},
			{Ref: "slot-1", Topic: "first", TargetDurationMs: 1000},
			{Ref: "slot-2", Topic: "second", TargetDurationMs: 1000},
		},
	}
	res := ports.ClipSamplerResult{
		ClipIDs: []string{"c3", "c1", "c2"},
	}
	got := BuildGemmaScenePlan(res, plan, 300)
	if got[0].SlotRef != "slot-3" || got[1].SlotRef != "slot-1" || got[2].SlotRef != "slot-2" {
		t.Errorf("plan.Slots order must be preserved; got %v", []string{got[0].SlotRef, got[1].SlotRef, got[2].SlotRef})
	}
	if got[0].SelectedClipRef != "c3" || got[1].SelectedClipRef != "c1" || got[2].SelectedClipRef != "c2" {
		t.Errorf("selected refs must mirror ClipIDs in declaration order; got %v", []string{got[0].SelectedClipRef, got[1].SelectedClipRef, got[2].SelectedClipRef})
	}
}
