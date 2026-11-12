package scriptgeneration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// overlayPlanBindingFixture is a Kaufmann-shaped plan: one phrase that the
// final-job policy rewrites (text_3d_* motions render an opaque white canvas)
// and one date card that it leaves alone.
func overlayPlanBindingFixture() *capmodel.OverlayPlan {
	return &capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        "plan-kaufmann",
		VideoID:       "video-kaufmann",
		Language:      "it",
		Width:         1920, Height: 1080,
		FPSNum: 24, FPSDen: 1,
		DurationMS: 5000,
		Items: []capmodel.OverlayItem{
			{
				ID:      "light-sweep-reveal__in-questa-fase-alcuni-dati__phrase__01",
				SceneID: "scene-1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE",
				Text: "in questa fase alcuni dati", PresetID: "phrase_default",
				MotionID: "text_3d_rotate", StartUS: 0, DurationUS: 1_000_000,
			},
			{
				ID:      "date__2007__date_number__01",
				SceneID: "scene-1", Kind: "number", TemplateID: "DATE_CARD",
				Text: "2007", StartUS: 1_000_000, DurationUS: 1_000_000,
			},
		},
	}
}

// manifestRefForPlan builds the render reference a manifest+timeline render of
// this plan would produce: one entry per item at the item's effective window.
func manifestRefForPlan(plan *capmodel.OverlayPlan, planDigest string) *RenderReference {
	entries := make([]capmodel.OverlayManifestEntry, 0, len(plan.Items))
	for _, item := range plan.Items {
		entries = append(entries, capmodel.OverlayManifestEntry{
			ItemID:      item.ID,
			InUS:        item.StartUSValue(),
			OutUS:       item.EndUSValue(),
			ContentHash: strings.Repeat("a", 64),
		})
	}
	return &RenderReference{
		JobID:      "job-overlay-render",
		Status:     "COMPLETED",
		PlanDigest: planDigest,
		Manifest:   &capmodel.OverlayManifest{Entries: entries},
	}
}

// TestFinalJobNormalizedPlanMatchesRenderedManifest is the first mandatory
// regression: the plan the renderer receives, the plan the checkpoint persists
// and the manifest derived from it must be ONE document.
//
// Before the fix the render path normalized a private snapshot, so the renderer
// executed a plan the checkpoint never saw. Here the enqueuer captures what the
// renderer actually receives and the test proves it is byte-identical (same
// digest) to the persisted plan, that the input plan was not mutated, and that
// a manifest derived from the pre-freeze plan is rejected.
func TestFinalJobNormalizedPlanMatchesRenderedManifest(t *testing.T) {
	plan := overlayPlanBindingFixture()
	result := &GenerateResult{OverlayPlan: plan, SourceLanguage: "it"}
	enqueuer := &recordingOverlayRenderEnqueuer{}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, log: zap.NewNop()}

	outcomes, err := runner.renderOverlayPlans(context.Background(),
		GenerateRequest{FinalJob: true, Render: scriptpkg.VideoRenderSpec{Enabled: true}}, 0, result)
	if err != nil {
		t.Fatalf("renderOverlayPlans: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].planDigest == "" {
		t.Fatalf("outcomes = %#v, want one render carrying a plan digest", outcomes)
	}

	frozen := result.OverlayPlan
	if frozen == nil {
		t.Fatal("the durable result lost its overlay plan")
	}
	if plan.Items[0].MotionID != "text_3d_rotate" {
		t.Fatalf("the input plan was mutated (motion = %q); freezing must not touch its argument", plan.Items[0].MotionID)
	}
	if frozen.Items[0].MotionID != "" || frozen.Items[0].PresetID != "static_text_smoke" {
		t.Fatalf("frozen phrase = motion %q preset %q, want the static final-job policy",
			frozen.Items[0].MotionID, frozen.Items[0].PresetID)
	}
	if frozen.Fingerprint != frozen.FingerprintValue() {
		t.Fatal("the frozen plan carries a stale fingerprint: Validate would reject the document the renderer just executed")
	}

	rendered := enqueuer.captured()
	if rendered == nil {
		t.Fatal("no plan reached the renderer")
	}
	if got, want := OverlayPlanRenderDigest(rendered), OverlayPlanRenderDigest(frozen); got != want {
		t.Fatalf("rendered plan digest = %s, persisted plan digest = %s: the renderer and the checkpoint diverged", got, want)
	}
	if outcomes[0].planDigest != OverlayPlanRenderDigest(frozen) {
		t.Fatalf("render reference digest = %s, want the persisted plan digest %s",
			outcomes[0].planDigest, OverlayPlanRenderDigest(frozen))
	}

	// The manifest built from the rendered plan binds; the same manifest built
	// from the pre-freeze plan does not.
	current := manifestRefForPlan(rendered, outcomes[0].planDigest)
	if err := ValidateOverlayPlanRenderBinding(frozen, current); err != nil {
		t.Fatalf("manifest of the rendered plan must bind to the persisted plan: %v", err)
	}
	stale := manifestRefForPlan(plan, OverlayPlanRenderDigest(plan))
	if err := ValidateOverlayPlanRenderBinding(frozen, stale); err == nil {
		t.Fatal("a manifest of the pre-freeze plan must not bind to the frozen plan")
	}
}

// TestFinalJobResumeRejectsOldPlanRender is the second mandatory regression: a
// retry must not adopt a render produced by a different plan version.
//
// The digest is the gate. A reference carrying another plan's digest is never
// reused; a reference carrying the current digest is; a legacy checkpoint with
// no digest is reused only when its content still covers the plan.
func TestFinalJobResumeRejectsOldPlanRender(t *testing.T) {
	plan := overlayPlanBindingFixture()
	result := &GenerateResult{OverlayPlan: plan}

	result.OverlayRender = manifestRefForPlan(plan, strings.Repeat("b", 64))
	pending := pendingOverlayPlans(result, GenerateRequest{})
	if len(pending) != 1 {
		t.Fatalf("pending plans = %d, want 1: a render from another plan version must be re-rendered", len(pending))
	}
	if err := ValidateOverlayPlanRenderBinding(plan, result.OverlayRender); err == nil {
		t.Fatal("a foreign plan digest must be reported as an incompatible render")
	}

	result.OverlayRender = manifestRefForPlan(plan, OverlayPlanRenderDigest(plan))
	if pending := pendingOverlayPlans(result, GenerateRequest{}); len(pending) != 0 {
		t.Fatalf("pending plans = %d, want 0 for a render of the current plan", len(pending))
	}

	// A checkpoint written before digest binding: reuse requires proof from the
	// content itself, which the full-coverage manifest provides.
	result.OverlayRender = manifestRefForPlan(plan, "")
	if pending := pendingOverlayPlans(result, GenerateRequest{}); len(pending) != 0 {
		t.Fatalf("pending plans = %d, want 0 for a digest-less but fully covering render", len(pending))
	}

	// Same legacy checkpoint, but missing an item: not provable, so re-render.
	partial := manifestRefForPlan(plan, "")
	partial.Manifest.Entries = partial.Manifest.Entries[:1]
	result.OverlayRender = partial
	if pending := pendingOverlayPlans(result, GenerateRequest{}); len(pending) != 1 {
		t.Fatalf("pending plans = %d, want 1 for a digest-less render that does not cover the plan", len(pending))
	}

	// The same IDs at a DIFFERENT window: the previous plan version kept the
	// names and moved the item, so ID coverage alone would accept it and ship a
	// manifest with the old placement.
	staleWindow := manifestRefForPlan(plan, "")
	staleWindow.Manifest.Entries[1].InUS += 500_000
	result.OverlayRender = staleWindow
	if pending := pendingOverlayPlans(result, GenerateRequest{}); len(pending) != 1 {
		t.Fatalf("pending plans = %d, want 1 for a digest-less render whose window no longer matches the plan", len(pending))
	}
}

// TestFinalJobResumeRepairReplaysAudioCompile proves the repair is reachable:
// the Kaufmann failure is a run that failed at PUBLISHING_DOCUMENTS while its
// sealed plan and its render disagree. Resuming there skips the overlay phase
// forever, so the repair must archive the reference AND move the resume point.
func TestFinalJobResumeRepairReplaysAudioCompile(t *testing.T) {
	plan := overlayPlanBindingFixture()
	result := &GenerateResult{OverlayPlan: plan}
	foreign := manifestRefForPlan(plan, strings.Repeat("b", 64))
	result.OverlayRender = foreign

	if got := overlayResumeStageAfterRepair(StagePublishingDocuments, FinalJobOverlayCheckpointRepair{}); got != StagePublishingDocuments {
		t.Fatalf("resume stage = %s without a repair, want the original stage", got)
	}

	report := RepairFinalJobOverlayCheckpoint(result)
	if !report.Repaired || result.OverlayRender != nil || len(result.OverlayRenderHistory) != 1 {
		t.Fatalf("repair = %#v, render = %#v, history = %d: want the reference archived and cleared",
			report, result.OverlayRender, len(result.OverlayRenderHistory))
	}
	if report.Archived == nil || report.Archived.PlanDigest != foreign.PlanDigest {
		t.Fatalf("archived = %#v, want the incompatible reference preserved for audit", report.Archived)
	}
	if got := overlayResumeStageAfterRepair(StagePublishingDocuments, report); got != StageCompilingAudio {
		t.Fatalf("resume stage = %s, want %s: a publish-stage retry would skip the overlay phase", got, StageCompilingAudio)
	}
	if got := overlayResumeStageAfterRepair(StageCompilingAudio, report); got != StageCompilingAudio {
		t.Fatalf("resume stage = %s, want the already-correct stage kept", got)
	}

	// Idempotent: a second pass neither re-archives nor changes anything.
	second := RepairFinalJobOverlayCheckpoint(result)
	if second.Repaired || len(result.OverlayRenderHistory) != 1 {
		t.Fatalf("second repair = %#v, history = %d: want a no-op", second, len(result.OverlayRenderHistory))
	}

	// A render that provably binds is never churned.
	valid := &GenerateResult{OverlayPlan: plan, OverlayRender: manifestRefForPlan(plan, OverlayPlanRenderDigest(plan))}
	keep := RepairFinalJobOverlayCheckpoint(valid)
	if keep.Repaired || valid.OverlayRender == nil {
		t.Fatalf("repair = %#v, render = %#v: a binding render must survive untouched", keep, valid.OverlayRender)
	}
}

// TestOverlayPlanDigestCoversPixelFieldsAndSurvivesCheckpoint pins both halves
// of the identity: every field that changes the pixels changes the digest, and
// the digest survives the durable JSON checkpoint unchanged.
func TestOverlayPlanDigestCoversPixelFieldsAndSurvivesCheckpoint(t *testing.T) {
	base := overlayPlanBindingFixture()
	baseline := OverlayPlanRenderDigest(base)
	if baseline == "" {
		t.Fatal("the fixture plan has no digest")
	}

	variants := []struct {
		name   string
		mutate func(*capmodel.OverlayPlan)
	}{
		{"entity variation seed", func(p *capmodel.OverlayPlan) { p.EntityVariationSeed = "seed-2" }},
		{"foreground scale", func(p *capmodel.OverlayPlan) { p.ForegroundScalePercent = 85 }},
		{"source video", func(p *capmodel.OverlayPlan) {
			p.Source = &capmodel.OverlaySource{AssetID: "asset-source", SHA256: strings.Repeat("c", 64)}
		}},
		{"background", func(p *capmodel.OverlayPlan) {
			p.Background = &capmodel.OverlayBackground{Kind: "color", Color: []float64{0.1, 0.2, 0.3}}
		}},
		{"item params", func(p *capmodel.OverlayPlan) {
			p.Items[0].Params = map[string]any{"speed": 2.0}
		}},
		{"caption motion", func(p *capmodel.OverlayPlan) { p.Items[0].CaptionMotionID = "fade" }},
	}
	for _, variant := range variants {
		mutated := cloneOverlayPlan(base)
		variant.mutate(mutated)
		if got := OverlayPlanRenderDigest(mutated); got == baseline {
			t.Fatalf("%s: digest unchanged (%s); the field is not covered by the identity", variant.name, got)
		}
	}

	// The durable checkpoint is a JSON round trip; a retry must be able to
	// recognise its own render after it.
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	var decoded capmodel.OverlayPlan
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if got := OverlayPlanRenderDigest(&decoded); got != baseline {
		t.Fatalf("digest after the checkpoint round trip = %s, want %s", got, baseline)
	}
}
