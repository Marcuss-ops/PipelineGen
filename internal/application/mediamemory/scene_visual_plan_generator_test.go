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

// fakeBindingsRepoForGenerator is the Fase 4.3 ListApprovedByConcept fake
// used by lookupPrimaryBinding-wired tests. godlike/06 SSOT
// (test seam narrow port doctrine): implements ONLY the read
// seam the Fase 4.3 fallback path uses. Upsert / Delete /
// FindByID / ListByConcept / ListByAsset panic so a future
// caller that accidentally exercises the write surface
// surfaces the drift loudly.
//
// listApprovedByConceptFn is an overridable function hook so
// each test can craft the exact return shape (one binding,
// multiple bindings, an error, ...).
type fakeBindingsRepoForGenerator struct {
	listApprovedByConceptFn func(ctx context.Context, conceptID string, slotKinds []SlotKind, limit int) ([]MediaBinding, error)
}

func (f *fakeBindingsRepoForGenerator) ListApprovedByConcept(ctx context.Context, conceptID string, slotKinds []SlotKind, limit int) ([]MediaBinding, error) {
	if f.listApprovedByConceptFn != nil {
		return f.listApprovedByConceptFn(ctx, conceptID, slotKinds, limit)
	}
	return nil, nil
}
func (f *fakeBindingsRepoForGenerator) Upsert(context.Context, MediaBinding) (MediaBinding, error) {
	panic("fakeBindingsRepo.Upsert: write surface not exercised in test scope")
}
func (f *fakeBindingsRepoForGenerator) FindByID(context.Context, string) (MediaBinding, error) {
	panic("fakeBindingsRepo.FindByID: write surface not exercised in test scope")
}
func (f *fakeBindingsRepoForGenerator) ListApprovedByConcepts(context.Context, []string, []SlotKind, int) (map[string][]MediaBinding, error) {
	panic("fakeBindingsRepo.ListApprovedByConcepts: unused in current test scope")
}
func (f *fakeBindingsRepoForGenerator) ListByConcept(context.Context, string) ([]MediaBinding, error) {
	panic("fakeBindingsRepo.ListByConcept: unused in current test scope")
}
func (f *fakeBindingsRepoForGenerator) ListByAsset(context.Context, string) ([]MediaBinding, error) {
	panic("fakeBindingsRepo.ListByAsset: unused in current test scope")
}
func (f *fakeBindingsRepoForGenerator) Delete(context.Context, string) error {
	panic("fakeBindingsRepo.Delete: unused in current test scope")
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

// ── Fase 4.3 tests ─────────────────────────────────────────────────

// ── Test 15: pickBindingForSlot filters by concept_id when SceneConcepts set ──

func TestGenerator_Generate_FiltersBySceneConcepts(t *testing.T) {
	// Two concepts, only one of which is in SceneConcepts.
	// Without filtering, pickBindingForSlot would return
	// the higher-scoring binding from concept-other; with
	// filtering, only concept-target is considered.
	gen := NewDefaultSceneVisualPlanGenerator(noopBindingRepository{}, NoopLogger(), nil)
	req := PlanGeneratorRequest{
		ProjectID:     "p1",
		Language:      "it",
		SceneConcepts: []string{"concept-target"},
		Scenes: []SceneSpec{
			{
				ID: "scene-1", Text: "Maya temples.", DurationMs: 10000, Language: "it",
				Slots: []SlotKind{SlotPrimaryVideo},
			},
		},
		ConceptBindings: map[string][]MediaBinding{
			"concept-target": {
				approvedBinding("bind-tgt", "asset-target", SlotPrimaryVideo, 0.7),
			},
			"concept-other": {
				approvedBinding("bind-other", "asset-other", SlotPrimaryVideo, 0.95), // higher score
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
	if got := out.Plans[0].Layers[0].AssetID; got != "asset-target" {
		t.Fatalf("expected asset-target (concept-target filter), got %q", got)
	}
}

// ── Test 16: pickBindingForSlot falls back to all concept_ids when SceneConcepts empty ──

func TestGenerator_Generate_NoSceneConcepts_FallsBackToAll(t *testing.T) {
	// Empty SceneConcepts → the pre-Fase-4.3 behaviour:
	// walk every concept_id and pick the highest-scoring
	// binding per slot.
	gen := NewDefaultSceneVisualPlanGenerator(noopBindingRepository{}, NoopLogger(), nil)
	req := PlanGeneratorRequest{
		ProjectID: "p1",
		Language:  "it",
		// SceneConcepts is empty / nil.
		Scenes: []SceneSpec{
			{
				ID: "scene-1", Text: "Maya temples.", DurationMs: 10000, Language: "it",
				Slots: []SlotKind{SlotPrimaryVideo},
			},
		},
		ConceptBindings: map[string][]MediaBinding{
			"concept-target": {
				approvedBinding("bind-tgt", "asset-target", SlotPrimaryVideo, 0.7),
			},
			"concept-other": {
				approvedBinding("bind-other", "asset-other", SlotPrimaryVideo, 0.95),
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
	// No filter → higher-scoring binding from concept-other wins.
	if got := out.Plans[0].Layers[0].AssetID; got != "asset-other" {
		t.Fatalf("expected asset-other (no filter, top-scoring wins), got %q", got)
	}
}

// ── Test 17: lookupPrimaryBinding wires to ListApprovedByConcept ──

func TestGenerator_Generate_LookupPrimaryBinding_WiresToRepository(t *testing.T) {
	// Empty ConceptBindings + non-empty SceneConcepts →
	// lookupPrimaryBinding MUST be invoked, which in turn
	// calls ListApprovedByConcept on each concept_id. The
	// fakeBindingsRepo returns one approved binding per
	// concept; the generator picks the top-scoring across
	// concepts.
	calls := make([]string, 0, 4)
	fake := &fakeBindingsRepoForGenerator{
		listApprovedByConceptFn: func(_ context.Context, conceptID string, slotKinds []SlotKind, _ int) ([]MediaBinding, error) {
			calls = append(calls, conceptID)
			// Concept-A returns a higher-scoring binding so
			// it should win.
			if conceptID == "concept-a" {
				return []MediaBinding{
					approvedBinding("bind-a", "asset-a", slotKinds[0], 0.95),
				}, nil
			}
			return []MediaBinding{
				approvedBinding("bind-b", "asset-b", slotKinds[0], 0.7),
			}, nil
		},
	}
	gen := NewDefaultSceneVisualPlanGenerator(fake, NoopLogger(), nil)
	req := PlanGeneratorRequest{
		ProjectID:     "p1",
		Language:      "it",
		SceneConcepts: []string{"concept-a", "concept-b"},
		Scenes: []SceneSpec{
			{
				ID: "scene-1", Text: "Maya temples.", DurationMs: 10000, Language: "it",
				Slots: []SlotKind{SlotPrimaryVideo},
			},
		},
		// ConceptBindings is nil → lookupPrimaryBinding is the
		// only path.
	}
	out, err := gen.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 ListApprovedByConcept calls (one per concept), got %d: %v", len(calls), calls)
	}
	if len(out.Plans) != 1 || len(out.Plans[0].Layers) != 1 {
		t.Fatalf("expected 1 plan with 1 layer, got %d plans", len(out.Plans))
	}
	if got := out.Plans[0].Layers[0].AssetID; got != "asset-a" {
		t.Fatalf("expected asset-a (higher-scoring concept), got %q", got)
	}
	if got := out.Plans[0].Layers[0].Provider; got != ProviderLocal {
		t.Fatalf("expected ProviderLocal (default binding provenance), got %q", got)
	}
}

// ── Test 18: deriveLayerProvider returns real binding.Provider ──

func TestGenerator_Generate_DeriveLayerProviderReturnsRealTag(t *testing.T) {
	// A binding with Provider=ProviderArtlist surfaces on
	// the resulting Layer as "artlist", not "local". This
	// enables the SceneVisualPlan.Source="mixed" branch
	// the Fase 4.2 forward-pin promised.
	gen := NewDefaultSceneVisualPlanGenerator(noopBindingRepository{}, NoopLogger(), nil)
	artlistBinding := approvedBinding("bind-art", "asset-artlist", SlotPrimaryVideo, 0.9)
	artlistBinding.Provider = ProviderArtlist
	req := PlanGeneratorRequest{
		ProjectID: "p1",
		Language:  "it",
		Scenes: []SceneSpec{
			{
				ID: "scene-1", Text: "Maya temples.", DurationMs: 10000, Language: "it",
				Slots: []SlotKind{SlotPrimaryVideo},
			},
		},
		ConceptBindings: map[string][]MediaBinding{
			"concept-maya": {artlistBinding},
		},
	}
	out, err := gen.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(out.Plans) != 1 || len(out.Plans[0].Layers) != 1 {
		t.Fatalf("expected 1 plan with 1 layer, got %d plans", len(out.Plans))
	}
	if got := out.Plans[0].Layers[0].Provider; got != ProviderArtlist {
		t.Fatalf("expected Provider=artlist (real binding provenance), got %q", got)
	}
}

// ── Test 19: SceneSpec.SceneConcepts overrides request-level filter ──

func TestGenerator_Generate_SceneSpecConceptsOverridesRequest(t *testing.T) {
	// Per-scene SceneSpec.SceneConcepts MUST take
	// precedence over the request-level SceneConcepts.
	gen := NewDefaultSceneVisualPlanGenerator(noopBindingRepository{}, NoopLogger(), nil)
	req := PlanGeneratorRequest{
		ProjectID:     "p1",
		Language:      "it",
		SceneConcepts: []string{"concept-default"},
		Scenes: []SceneSpec{
			{
				ID:            "scene-1",
				Text:          "Maya temples.",
				DurationMs:    10000,
				Language:      "it",
				SceneConcepts: []string{"concept-override"},
				Slots:         []SlotKind{SlotPrimaryVideo},
			},
		},
		ConceptBindings: map[string][]MediaBinding{
			"concept-default": {
				approvedBinding("bind-d", "asset-default", SlotPrimaryVideo, 0.95),
			},
			"concept-override": {
				approvedBinding("bind-o", "asset-override", SlotPrimaryVideo, 0.7),
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
	if got := out.Plans[0].Layers[0].AssetID; got != "asset-override" {
		t.Fatalf("expected asset-override (scene-level filter), got %q", got)
	}
}

// ── Test 20: deriveLayerProvider returns ProviderLocal for empty binding.Provider ──

func TestDeriveLayerProvider_EmptyProviderDefaultsToLocal(t *testing.T) {
	if got := deriveLayerProvider(&MediaBinding{Provider: ""}); got != ProviderLocal {
		t.Fatalf("expected ProviderLocal for empty Provider, got %q", got)
	}
	if got := deriveLayerProvider(nil); got != ProviderLocal {
		t.Fatalf("expected ProviderLocal for nil binding, got %q", got)
	}
	if got := deriveLayerProvider(&MediaBinding{Provider: ProviderSemanticIndex}); got != ProviderSemanticIndex {
		t.Fatalf("expected ProviderSemanticIndex verbatim, got %q", got)
	}
}
