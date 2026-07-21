// Package mediamemory — scene_visual_plan_generator_test.go pins
// the Fase 4.2 contract:
//
//  1. SceneVisualPlanGenerator.Generate produces 1-3 layers per
//     scene (primary_video + secondary_image + evidence_overlay)
//     from the canonical binding graph.
//  2. LayoutKind closed-set: primary_video → fullscreen,
//     secondary_image → right_panel, evidence_overlay →
//     fullscreen_fade (per DefaultLayoutForSlot SSOT).
//  3. FitLayerWindow projects a binding [StartMs, EndMs] onto
//     [0, sceneDurationMs] using the godlike/06 SSOT (binding
//     window first, scene-fraction fallback).
//  4. PlanEnvelope JSON roundtrip via SerializePlans/ParsePlans
//     preserves all canonical fields.
//  5. ParsePlans rejects schema_version drift with
//     ErrPlanSchemaDrift (typed sentinel).
//  6. Same input → same plan slice order (deterministic
//     generator contract).
//  7. Missing binding surfaces Warning, not silent zero-output.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// ── Helpers ────────────────────────────────────────────────────────

func approvedBinding(id, assetID string, slot SlotKind, score float64) MediaBinding {
	return MediaBinding{
		ID:             id,
		AssetID:        assetID,
		SlotKind:       slot,
		Origin:         OriginManual,
		ApprovalStatus: ApprovalApproved,
		SuccessScore:   score,
		ManualScore:    0.4,
	}
}

// noopBindingRepository is the canonical test fake for
// BindingRepository. godlike/06 SSOT (test seam narrow port
// doctrine): the fake implements ONLY the seam the generator
// uses. Tests that don't exercise the repository inject this
// fake so the constructor's fail-closed nil-check passes.
//
// The current generator wires BindingRepository for the
// fallback path (Fase 4.3+ — lookupPrimaryBinding is a stub).
// Tests 6-9 populate ConceptBindings directly and never call
// into the repository, so the fake's methods are inert.
//
// Any future test that DOES exercise the repository MUST
// replace this fake with one that records calls — panicking
// keeps the wiring drift visible.
type noopBindingRepository struct{}

func (noopBindingRepository) Upsert(context.Context, MediaBinding) (MediaBinding, error) {
	panic("noopBindingRepository.Upsert: unused in current test scope")
}
func (noopBindingRepository) FindByID(context.Context, string) (MediaBinding, error) {
	panic("noopBindingRepository.FindByID: unused in current test scope")
}
func (noopBindingRepository) ListApprovedByConcept(context.Context, string, []SlotKind, int) ([]MediaBinding, error) {
	panic("noopBindingRepository.ListApprovedByConcept: unused in current test scope")
}
func (noopBindingRepository) ListApprovedByConcepts(context.Context, []string, []SlotKind, int) (map[string][]MediaBinding, error) {
	panic("noopBindingRepository.ListApprovedByConcepts: unused in current test scope")
}
func (noopBindingRepository) ListByConcept(context.Context, string) ([]MediaBinding, error) {
	panic("noopBindingRepository.ListByConcept: unused in current test scope")
}
func (noopBindingRepository) ListByAsset(context.Context, string) ([]MediaBinding, error) {
	panic("noopBindingRepository.ListByAsset: unused in current test scope")
}
func (noopBindingRepository) Delete(context.Context, string) error {
	panic("noopBindingRepository.Delete: unused in current test scope")
}

// ── Test 1: DefaultLayoutForSlot canonical mapping ────────────────

func TestDefaultLayoutForSlot_CanonicalMapping(t *testing.T) {
	cases := map[SlotKind]LayoutKind{
		SlotPrimaryVideo:    LayoutFullscreen,
		SlotSecondaryImage:  LayoutRightPanel,
		SlotEvidenceOverlay: LayoutFullscreenFade,
		SlotMap:             LayoutFullscreenFade,
		SlotPortrait:        LayoutRightPanel,
		SlotDocument:        LayoutLowerThird,
		SlotBackground:      LayoutFullscreenFade,
	}
	for slot, want := range cases {
		if got := DefaultLayoutForSlot(slot); got != want {
			t.Fatalf("DefaultLayoutForSlot(%q) = %q, want %q", slot, got, want)
		}
	}
}

// ── Test 2: FitLayerWindow binding-first, fraction-fallback ──────

func TestFitLayerWindow_BindingFirst(t *testing.T) {
	// 10000ms scene; binding 2000..6000 → use verbatim.
	s, e := FitLayerWindow(SlotPrimaryVideo, 2000, 6000, 10000)
	if s != 2000 || e != 6000 {
		t.Fatalf("expected verbatim binding window (2000,6000), got (%d,%d)", s, e)
	}
}

// ── Test 3: FitLayerWindow clamps when binding exceeds scene ──────

func TestFitLayerWindow_ClampsWhenBindingExceedsScene(t *testing.T) {
	// 5000ms scene; binding 1000..9000 → clamp end to 5000.
	s, e := FitLayerWindow(SlotPrimaryVideo, 1000, 9000, 5000)
	if s != 1000 || e != 5000 {
		t.Fatalf("expected clamp (1000,5000), got (%d,%d)", s, e)
	}
}

// ── Test 4: FitLayerWindow fallback to slot canonical fraction ──

func TestFitLayerWindow_FallbackToCanonicalFractionWhenBindingZero(t *testing.T) {
	// 10000ms scene; secondary_image zero-zero binding → 60%→95%.
	s, e := FitLayerWindow(SlotSecondaryImage, 0, 0, 10000)
	if s != 6000 || e != 9500 {
		t.Fatalf("expected canonical-fraction fallback (6000,9500), got (%d,%d)", s, e)
	}
}

// ── Test 5: Layout closed-set predicate ────────────────────────────

func TestIsKnownLayout_ClosedSet(t *testing.T) {
	known := []LayoutKind{
		LayoutFullscreen, LayoutFullscreenFade, LayoutRightPanel,
		LayoutLowerThird, LayoutSplitScreen, LayoutPictureInPicture,
	}
	for _, k := range known {
		if !IsKnownLayout(k) {
			t.Fatalf("expected %q to be in canonical closed set", k)
		}
	}
	if IsKnownLayout("unknown_layout") {
		t.Fatal("expected unknown layout to be rejected")
	}
}

// ── Test 6: Generate produces 3-layer plan ─────────────────────────

func TestGenerator_Generate_3LayerPlan(t *testing.T) {
	gen := NewDefaultSceneVisualPlanGenerator(noopBindingRepository{}, NoopLogger(), nil)
	req := PlanGeneratorRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "scene-1", Text: "Maya temples.", DurationMs: 10000, Language: "it"},
		},
		Policy: ResolvePolicy{},
		ConceptBindings: map[string][]MediaBinding{
			"concept-maya": {
				approvedBinding("bind-pv", "asset-maya-temple", SlotPrimaryVideo, 0.9),
				approvedBinding("bind-si", "asset-maya-drawing", SlotSecondaryImage, 0.7),
				approvedBinding("bind-eo", "asset-maya-evidence", SlotEvidenceOverlay, 0.6),
			},
		},
	}
	out, err := gen.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(out.Plans))
	}
	plan := out.Plans[0]
	if len(plan.Layers) != 3 {
		t.Fatalf("expected 3 layers, got %d", len(plan.Layers))
	}
	if plan.Layers[0].Slot != SlotPrimaryVideo ||
		plan.Layers[1].Slot != SlotSecondaryImage ||
		plan.Layers[2].Slot != SlotEvidenceOverlay {
		t.Fatalf("slot order drift: %v %v %v",
			plan.Layers[0].Slot, plan.Layers[1].Slot, plan.Layers[2].Slot)
	}
	if plan.Layers[0].Layout != string(LayoutFullscreen) ||
		plan.Layers[1].Layout != string(LayoutRightPanel) ||
		plan.Layers[2].Layout != string(LayoutFullscreenFade) {
		t.Fatalf("layout drift: %v %v %v",
			plan.Layers[0].Layout, plan.Layers[1].Layout, plan.Layers[2].Layout)
	}
	if plan.Source != "local" {
		t.Fatalf("expected source=local (all local approved bindings), got %q", plan.Source)
	}
}

// ── Test 7: Generate 1-layer fallback when only primary_video ─────

func TestGenerator_Generate_1LayerPlan(t *testing.T) {
	gen := NewDefaultSceneVisualPlanGenerator(noopBindingRepository{}, NoopLogger(), nil)
	req := PlanGeneratorRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "scene-1", Text: "Maya temples.", DurationMs: 10000, Language: "it"},
		},
		ConceptBindings: map[string][]MediaBinding{
			"concept-maya": {
				approvedBinding("bind-pv", "asset-maya-temple", SlotPrimaryVideo, 0.9),
			},
		},
	}
	out, err := gen.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Plans) != 1 || len(out.Plans[0].Layers) != 1 {
		t.Fatalf("expected 1 plan with 1 layer, got %d plans", len(out.Plans))
	}
	if len(out.Warnings) < 2 {
		t.Fatalf("expected warnings for missing secondary_image + evidence_overlay, got %v", out.Warnings)
	}
}

// ── Test 8: Generate missing-binding surfaces Warning, no silent zero ─

func TestGenerator_Generate_MissingBinding_SurfacesWarning(t *testing.T) {
	gen := NewDefaultSceneVisualPlanGenerator(noopBindingRepository{}, NoopLogger(), nil)
	req := PlanGeneratorRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{ID: "scene-empty", Text: "no bindings.", DurationMs: 5000, Language: "it"},
		},
		// No ConceptBindings → 0-layer plan, all 3 slots missing.
	}
	out, err := gen.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(out.Plans))
	}
	if len(out.Plans[0].Layers) != 0 {
		t.Fatalf("expected 0 layers (no fake-availability), got %d", len(out.Plans[0].Layers))
	}
	if len(out.Warnings) == 0 {
		t.Fatal("expected Warnings[] entries for missing bindings")
	}
}

// ── Test 9: Determinism — same input → same output ────────────────

func TestGenerator_Generate_DeterministicOutput(t *testing.T) {
	gen := NewDefaultSceneVisualPlanGenerator(noopBindingRepository{}, NoopLogger(), nil)
	mk := func() PlanGeneratorRequest {
		return PlanGeneratorRequest{
			ProjectID: "p",
			Language:  "it",
			Scenes: []SceneSpec{
				{ID: "s1", Text: "T", DurationMs: 8000, Language: "it"},
				{ID: "s2", Text: "U", DurationMs: 12000, Language: "it"},
			},
			ConceptBindings: map[string][]MediaBinding{
				"c1": {
					approvedBinding("b1", "a1", SlotPrimaryVideo, 0.9),
					approvedBinding("b2", "a2", SlotSecondaryImage, 0.7),
				},
				"c2": {
					approvedBinding("b3", "a3", SlotEvidenceOverlay, 0.6),
				},
			},
		}
	}
	a, _ := gen.Generate(context.Background(), mk())
	b, _ := gen.Generate(context.Background(), mk())
	if len(a.Plans) != len(b.Plans) {
		t.Fatalf("plan count drift: %d vs %d", len(a.Plans), len(b.Plans))
	}
	for i := range a.Plans {
		pa, pb := a.Plans[i], b.Plans[i]
		if pa.SceneID != pb.SceneID {
			t.Fatalf("scene[%d] ID drift: %q vs %q", i, pa.SceneID, pb.SceneID)
		}
		if len(pa.Layers) != len(pb.Layers) {
			t.Fatalf("scene[%d] layer count drift: %d vs %d", i, len(pa.Layers), len(pb.Layers))
		}
		for j := range pa.Layers {
			if pa.Layers[j].Slot != pb.Layers[j].Slot ||
				pa.Layers[j].AssetID != pb.Layers[j].AssetID ||
				pa.Layers[j].Layout != pb.Layers[j].Layout {
				t.Fatalf("scene[%d] layer[%d] drift: %+v vs %+v",
					i, j, pa.Layers[j], pb.Layers[j])
			}
		}
	}
}

// ── Test 10: nil bindings repository → ErrSemanticNotConfigured ─

func TestGenerator_Generate_NilBindingsRepositoryFailsClosed(t *testing.T) {
	g := &defaultSceneVisualPlanGenerator{bindings: nil, log: NoopLogger(), clock: nil}
	_, err := g.Generate(context.Background(), PlanGeneratorRequest{
		Scenes: []SceneSpec{{ID: "s", Text: "T", DurationMs: 1000}},
	})
	if !errors.Is(err, ErrSemanticNotConfigured) {
		t.Fatalf("expected ErrSemanticNotConfigured, got %v", err)
	}
}

// ── Test 11: JSON wire roundtrip ──────────────────────────────────

func TestSerializePlans_ParsePlans_Roundtrip(t *testing.T) {
	plans := []SceneVisualPlan{
		{
			ProjectID: "p1", SceneID: "s1", Text: "Maya", Language: "it",
			DurationMs: 8000,
			Layers: []Layer{
				{Slot: SlotPrimaryVideo, AssetID: "a1", BindingID: "b1",
					StartMs: 0, EndMs: 8000, Layout: string(LayoutFullscreen),
					CandidateScore: 0.95, Provider: ""},
				{Slot: SlotSecondaryImage, AssetID: "a2", BindingID: "b2",
					StartMs: 4800, EndMs: 7600, Layout: string(LayoutRightPanel),
					CandidateScore: 0.78, Provider: ""},
				{Slot: SlotEvidenceOverlay, AssetID: "a3", BindingID: "b3",
					StartMs: 3200, EndMs: 7600, Layout: string(LayoutFullscreenFade),
					CandidateScore: 0.65, Provider: ""},
			},
			Source: "local",
		},
	}
	raw, err := SerializePlans("p1", plans)
	if err != nil {
		t.Fatalf("SerializePlans: %v", err)
	}
	projectID, parsed, err := ParsePlans(raw)
	if err != nil {
		t.Fatalf("ParsePlans: %v", err)
	}
	if projectID != "p1" {
		t.Fatalf("project_id drift: %q", projectID)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(parsed))
	}
	got := parsed[0]
	if got.SceneID != plans[0].SceneID {
		t.Fatalf("scene_id drift: %q vs %q", got.SceneID, plans[0].SceneID)
	}
	if len(got.Layers) != len(plans[0].Layers) {
		t.Fatalf("layer count drift: %d vs %d", len(got.Layers), len(plans[0].Layers))
	}
	for i, l := range got.Layers {
		want := plans[0].Layers[i]
		if l.Slot != want.Slot || l.AssetID != want.AssetID || l.Layout != want.Layout ||
			l.StartMs != want.StartMs || l.EndMs != want.EndMs {
			t.Fatalf("layer[%d] drift: %+v vs %+v", i, l, want)
		}
	}
	// envelope must carry the canonical schema version.
	if !strings.Contains(string(raw), `"schema_version":"v1"`) {
		t.Fatalf("missing schema_version=v1 in wire envelope: %s", raw)
	}
}

// ── Test 12: Schema drift → ErrPlanSchemaDrift ────────────────────

func TestParsePlans_SchemaDriftReturnsTypedSentinel(t *testing.T) {
	// Hand-crafted envelope with v2 schema → ParsePlans MUST
	// reject with wrapped ErrPlanSchemaDrift.
	bad := []byte(`{"schema_version":"v2","project_id":"p","plans":[]}`)
	_, _, err := ParsePlans(bad)
	if !errors.Is(err, ErrPlanSchemaDrift) {
		t.Fatalf("expected wrapped ErrPlanSchemaDrift, got %v", err)
	}
}

// ── Test 13: ParsePlans unknown slot → ErrInvalidSlotKind ──────────

func TestParsePlans_UnknownSlotReturnsInvalidSlotKind(t *testing.T) {
	bad := []byte(`{"schema_version":"v1","project_id":"p","plans":[{"project_id":"p","scene_id":"s","text":"t","language":"it","duration_ms":1000,"layers":[{"slot":"unknown_slot","asset_id":"a","binding_id":"b","start_ms":0,"end_ms":1000,"layout":"fullscreen","candidate_score":0,"provider":""}],"source":"local"}]}`)
	_, _, err := ParsePlans(bad)
	if !errors.Is(err, ErrInvalidSlotKind) {
		t.Fatalf("expected wrapped ErrInvalidSlotKind, got %v", err)
	}
}

// ── Test 14: ParsePlans unknown layout surfaces error ────────────

func TestParsePlans_UnknownLayoutReturnsError(t *testing.T) {
	bad := []byte(fmt.Sprintf(
		`{"schema_version":"v1","project_id":"p","plans":[{"project_id":"p","scene_id":"s","text":"t","language":"it","duration_ms":1000,"layers":[{"slot":"%s","asset_id":"a","binding_id":"b","start_ms":0,"end_ms":1000,"layout":"unknown_layout","candidate_score":0,"provider":""}],"source":"local"}]}`,
		string(SlotPrimaryVideo)))
	_, _, err := ParsePlans(bad)
	if err == nil {
		t.Fatal("expected error on unknown layout")
	}
}
