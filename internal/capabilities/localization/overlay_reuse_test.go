package localization

// overlay_reuse_test.go — the multi-language overlay REUSE contract.
//
// One certified overlay render per semantic item is reused by every language
// variant of a source: the lineages are language-independent, so all N plans
// carry the same identities while the variants stay distinct (language +
// subtitle track + style). These cases pin that reuse, the fingerprint's
// reaction to a changed or DROPPED overlay, and the all-or-nothing lineage gate.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// staticTrackResolver resolves one fixed READY track per language and counts the
// resolutions, so a test can prove the fan-out resolves the source transcript
// once and each target track once.
type staticTrackResolver struct {
	tracks map[string]*TrackRef
	calls  int
}

func (r *staticTrackResolver) ResolveTrack(_ context.Context, assetID, language string, _ detail.TextTrackKind) (*TrackRef, error) {
	r.calls++
	ref := r.tracks[language]
	if ref == nil {
		return nil, fmt.Errorf("no READY track for %s/%s", assetID, language)
	}
	return ref, nil
}

// testOverlayLineages returns the TWO overlays one scene composites: production
// renders one short video per semantic overlay item, so a scene carrying a
// phrase AND an entity card declares two lineages.
func testOverlayLineages() []cliprender.OverlayRefSpec {
	return []cliprender.OverlayRefSpec{
		{
			RenderJobID:        "overlay-job-1",
			PlanFingerprint:    strings.Repeat("c", 64),
			RenderKey:          strings.Repeat("e", 64),
			SourceVideoAssetID: "source-asset-1",
			StartUS:            2_000_000,
			EndUS:              5_500_000,
		},
		{
			RenderJobID:        "overlay-job-2",
			PlanFingerprint:    strings.Repeat("c", 64),
			RenderKey:          strings.Repeat("d", 64),
			SourceVideoAssetID: "source-asset-1",
			StartUS:            6_000_000,
			EndUS:              8_000_000,
		},
	}
}

func testSourceInput(overlays []cliprender.OverlayRefSpec) SourceInput {
	return SourceInput{
		JobID:             "job-1",
		SceneID:           "scene-1",
		AssetID:           "source-asset-1",
		SourceLanguage:    "en",
		SourceSHA256:      strings.Repeat("a", 64),
		DurationMS:        8432,
		OutputProfileHash: "profile-sha",
		RendererVersion:   "renderer-v1",
		Overlays:          overlays,
	}
}

// TestPlanBuilder_ReusesOneOverlayAcrossLanguages is the reuse proof: three
// languages produce three DISTINCT variants that all carry the SAME overlay
// identities, i.e. one overlay render per item serves the whole fan-out instead
// of one per language. With production's one-short-video-per-item contract, a
// single-lineage model would have silently dropped the second overlay.
func TestPlanBuilder_ReusesOneOverlayAcrossLanguages(t *testing.T) {
	resolver := &staticTrackResolver{tracks: map[string]*TrackRef{
		"en": {TrackID: 101, SHA256: strings.Repeat("1", 64)},
		"es": {TrackID: 202, SHA256: strings.Repeat("2", 64)},
		"de": {TrackID: 303, SHA256: strings.Repeat("3", 64)},
	}}
	builder, err := NewLocalizationPlanBuilder(resolver)
	if err != nil {
		t.Fatalf("NewLocalizationPlanBuilder: %v", err)
	}

	lineages := testOverlayLineages()
	plans, err := builder.Build(context.Background(), testSourceInput(lineages), []LanguageRequest{
		{Language: "en"}, {Language: "es"}, {Language: "de"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plans) != 3 {
		t.Fatalf("plans: got %d, want 3", len(plans))
	}

	for i, plan := range plans {
		if len(plan.Overlays) != len(lineages) {
			t.Fatalf("plan %d (%s): got %d overlays, want all %d", i, plan.TargetLanguage, len(plan.Overlays), len(lineages))
		}
		for j := range lineages {
			if plan.Overlays[j] != lineages[j] {
				t.Fatalf("plan %d: overlay %d drifted: %+v", i, j, plan.Overlays[j])
			}
		}
		if plan.Validate() != nil {
			t.Fatalf("plan %d: Validate: %v", i, plan.Validate())
		}
	}

	// Variants stay distinct: reuse of the overlays must never collapse the
	// languages into one artifact (only the overlays are shared).
	if plans[0].Fingerprint == plans[1].Fingerprint || plans[1].Fingerprint == plans[2].Fingerprint {
		t.Fatalf("language variants collapsed onto one fingerprint")
	}

	// The source transcript is resolved once; each target track once.
	if resolver.calls != len(plans) {
		t.Fatalf("track resolutions: got %d, want %d", resolver.calls, len(plans))
	}
}

// TestPlanBuilder_WithoutOverlayKeepsPlansOverlayFree pins that a run without an
// entity overlay does not acquire one by accident.
func TestPlanBuilder_WithoutOverlayKeepsPlansOverlayFree(t *testing.T) {
	resolver := &staticTrackResolver{tracks: map[string]*TrackRef{
		"en": {TrackID: 101, SHA256: strings.Repeat("1", 64)},
		"es": {TrackID: 202, SHA256: strings.Repeat("2", 64)},
	}}
	builder, err := NewLocalizationPlanBuilder(resolver)
	if err != nil {
		t.Fatalf("NewLocalizationPlanBuilder: %v", err)
	}
	plans, err := builder.Build(context.Background(), testSourceInput(nil), []LanguageRequest{{Language: "en"}, {Language: "es"}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i, plan := range plans {
		if len(plan.Overlays) != 0 {
			t.Fatalf("plan %d: unexpected overlay lineages %+v", i, plan.Overlays)
		}
	}
}

// TestValidate_OverlayLineageIsAllOrNothing pins the fail-closed gate: a partial
// lineage — whether it is the only one or ONE among complete ones — or an
// invalid window is rejected instead of being silently dropped.
func TestValidate_OverlayLineageIsAllOrNothing(t *testing.T) {
	complete := testOverlayLineages()[0]

	cases := []struct {
		name     string
		overlays []cliprender.OverlayRefSpec
		wantErr  bool
	}{
		{"absent", nil, false},
		{"complete", []cliprender.OverlayRefSpec{complete}, false},
		{"missing render job", []cliprender.OverlayRefSpec{{PlanFingerprint: complete.PlanFingerprint, RenderKey: complete.RenderKey, SourceVideoAssetID: complete.SourceVideoAssetID, EndUS: complete.EndUS}}, true},
		{"missing plan fingerprint", []cliprender.OverlayRefSpec{{RenderJobID: complete.RenderJobID, RenderKey: complete.RenderKey, SourceVideoAssetID: complete.SourceVideoAssetID, EndUS: complete.EndUS}}, true},
		{"missing render key", []cliprender.OverlayRefSpec{{RenderJobID: complete.RenderJobID, PlanFingerprint: complete.PlanFingerprint, SourceVideoAssetID: complete.SourceVideoAssetID, EndUS: complete.EndUS}}, true},
		{"missing source video", []cliprender.OverlayRefSpec{{RenderJobID: complete.RenderJobID, PlanFingerprint: complete.PlanFingerprint, RenderKey: complete.RenderKey, EndUS: complete.EndUS}}, true},
		{"empty window", []cliprender.OverlayRefSpec{{RenderJobID: complete.RenderJobID, PlanFingerprint: complete.PlanFingerprint, RenderKey: complete.RenderKey, SourceVideoAssetID: complete.SourceVideoAssetID, StartUS: 5_000_000, EndUS: 5_000_000}}, true},
		{"negative start", []cliprender.OverlayRefSpec{{RenderJobID: complete.RenderJobID, PlanFingerprint: complete.PlanFingerprint, RenderKey: complete.RenderKey, SourceVideoAssetID: complete.SourceVideoAssetID, StartUS: -1, EndUS: 5_000_000}}, true},
		// The partial lineage is the SECOND of two: the gate must reject the
		// plan rather than composite only the first overlay.
		{"partial among complete", []cliprender.OverlayRefSpec{complete, {RenderJobID: "job-2", PlanFingerprint: complete.PlanFingerprint, SourceVideoAssetID: complete.SourceVideoAssetID, EndUS: 5_000_000}}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := validPlan()
			plan.Overlays = tc.overlays
			plan.Fingerprint = Fingerprint(plan)
			err := plan.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected a validation error")
			}
			if tc.wantErr && !errors.Is(err, ErrInvalidLocalizedClipPlan) {
				t.Fatalf("error must wrap ErrInvalidLocalizedClipPlan, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestFingerprint_ChangesWithOverlay pins that a variant whose reused overlays
// changed — or that LOST one of them — is a different artifact (never mistaken
// for the cached one), while identical overlays keep the digest stable.
func TestFingerprint_ChangesWithOverlay(t *testing.T) {
	without := basePlan()
	withOverlay := basePlan()
	withOverlay.Overlays = testOverlayLineages()

	if Fingerprint(without) == Fingerprint(withOverlay) {
		t.Fatalf("an overlay-bearing variant must not share the overlay-free digest")
	}
	if Fingerprint(withOverlay) != Fingerprint(withOverlay) {
		t.Fatalf("fingerprint must be deterministic")
	}

	// A different render key on ANY of the overlays invalidates the digest.
	changed := basePlan()
	other := testOverlayLineages()
	other[1].RenderKey = strings.Repeat("f", 64)
	changed.Overlays = other
	if Fingerprint(withOverlay) == Fingerprint(changed) {
		t.Fatalf("a different overlay render must invalidate the variant digest")
	}

	// Dropping one declared overlay is a DIFFERENT artifact, not the cached
	// full-overlay variant: a clip that composite two of three overlays must
	// never be served from the three-overlay cache entry.
	dropped := basePlan()
	dropped.Overlays = testOverlayLineages()[:1]
	if Fingerprint(withOverlay) == Fingerprint(dropped) {
		t.Fatalf("a variant missing a declared overlay must not share the full digest")
	}

	// Declared ORDER is part of the identity: the same overlays in a different
	// order composite at different windows, so the digests must differ.
	reordered := basePlan()
	swapped := testOverlayLineages()
	swapped[0], swapped[1] = swapped[1], swapped[0]
	reordered.Overlays = swapped
	if Fingerprint(withOverlay) == Fingerprint(reordered) {
		t.Fatalf("reordering the declared overlays must change the variant digest")
	}

	// The same overlays across languages still separate the variants: the
	// overlays are shared, the language is not.
	spanish := basePlan()
	spanish.Overlays = testOverlayLineages()
	german := basePlan()
	german.Overlays = testOverlayLineages()
	german.TargetLanguage = "de"
	if Fingerprint(spanish) == Fingerprint(german) {
		t.Fatalf("languages must stay distinct even when they share overlays")
	}
}
