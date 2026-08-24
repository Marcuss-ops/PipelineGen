// Package stockpipeline — step_plan_clips_test.go (July 2026).
//
// TDD contract tests for StockPlanStep clip normalisation.
// When explicit clips are provided without timestamps
// (StartSec=0, EndSec=0) or with only start (StartSec>0,
// EndSec=0), the step normalises them before passing to
// the explicit planner so the cutter gets valid non-zero
// ranges instead of passing Start=0 End=0 to ffmpeg.
//
// godlike/07 typed-error contract: each test exercises a
// single normalisation edge case and asserts the output
// plan contains the expected StartSec/EndSec values.
package stockpipeline

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── fakeStepRunner ─────────────────────────────────────────────────

// fakeStepRunner is a minimal StepRunner stub used by the
// StockPlanStep tests. Only the methods accessed by the plan
// step are implemented; the rest return zero values.
type fakeStepRunner struct {
	runInput *RunInput
	cfg      OrchestratorConfig
	state    *RunState
	planner  ClipPlanner
	cutter   VideoCutter
}

func (f *fakeStepRunner) Cfg() OrchestratorConfig                                      { return f.cfg }
func (f *fakeStepRunner) RunInput() *RunInput                                          { return f.runInput }
func (f *fakeStepRunner) JobID() string                                                { return "test-job" }
func (f *fakeStepRunner) PolicyVersion() string                                        { return f.cfg.PolicyVersion }
func (f *fakeStepRunner) Planner() ClipPlanner                                         { return f.planner }
func (f *fakeStepRunner) SourceStager() acquisition.SourceStager                       { return nil }
func (f *fakeStepRunner) Cutter() VideoCutter                                          { return f.cutter }
func (f *fakeStepRunner) Renderer() StockRenderer                                      { return nil }
func (f *fakeStepRunner) Builder() ManifestBuilder                                     { return nil }
func (f *fakeStepRunner) Writer() TransactionalAssetWriter                             { return nil }
func (f *fakeStepRunner) Projection() ProjectionPort                                   { return nil }
func (f *fakeStepRunner) SourceDurationProbe() SourceDurationProbe                     { return nil }
func (f *fakeStepRunner) ArtifactPreparation() finalization.ArtifactPreparationService { return nil }
func (f *fakeStepRunner) JobFinalizer() finalization.JobFinalizer                      { return nil }
func (f *fakeStepRunner) RunFingerprint() string                                       { return "test-fingerprint" }
func (f *fakeStepRunner) Log() *zap.Logger                                             { return zap.NewNop() }
func (f *fakeStepRunner) LocalFS() LocalFSPort                                         { return newRealishFakeLocalFS() }
func (f *fakeStepRunner) State() *RunState                                             { return f.state }
func (f *fakeStepRunner) BatchRepository() StockBatchRepository                        { return nil }

// newFakeRunner builds a fakeStepRunner with the given clips,
// clip duration, and policy version. The planner is the
// deterministic planner (never called when clips are present,
// but required for interface satisfaction).
func newFakeRunner(clips []ClipSpec, clipDur int, policyVer string) *fakeStepRunner {
	if policyVer == "" {
		policyVer = "test-policy-v1"
	}
	return &fakeStepRunner{
		runInput: &RunInput{
			Clips:        clips,
			ClipDuration: clipDur,
			TotalMinutes: 1,
		},
		cfg: OrchestratorConfig{
			PolicyVersion: policyVer,
		},
		state:   &RunState{},
		planner: NewDeterministicPlanner(),
	}
}

// TestExecuteCuts_LeavesEncoderPolicyToInfrastructure pins the stock.extract_clips
// request assembly to the neutral port contract. The configured infrastructure
// cutter owns the single auto/NVENC/libx264 decision.
func TestExecuteCuts_UsesCanonicalCPUCodec(t *testing.T) {
	const sourceID = "https://example.com/source.mp4"
	cutter := &mockCutter{
		res: CutBatchResult{
			SourcePath: "/tmp/source.mp4",
			Items: []CutItemResult{{
				JobID:      "/tmp/cut.mp4",
				OutputPath: "/tmp/cut.mp4",
				Status:     CutItemStatusSucceeded,
			}},
		},
	}
	runner := newFakeRunner(nil, 5, "")
	runner.cutter = cutter
	runner.state.StagedAssets = []*assets.StagedAsset{{
		SourceID:    sourceID,
		LocalPath:   "/tmp/source.mp4",
		DurationSec: 10,
	}}

	_, err := executeCuts(context.Background(), runner, sourceID, "/tmp/source.mp4", 10, []ClipPlan{{
		SourceID: sourceID,
		StartSec: 0,
		EndSec:   5,
	}}, 0, true)
	if err != nil {
		t.Fatalf("executeCuts returned unexpected error: %v", err)
	}

	canonical := DefaultPipelineConfig()
	if cutter.lastReq.Codec != "" || cutter.lastReq.Preset != canonical.Preset || cutter.lastReq.CRF != canonical.CRF {
		t.Fatalf("executeCuts encoder policy = (%q, %q, %d), want neutral codec with canonical preset/CRF (%q, %q, %d)",
			cutter.lastReq.Codec, cutter.lastReq.Preset, cutter.lastReq.CRF,
			"", canonical.Preset, canonical.CRF)
	}
}

// ── Normalisation tests ────────────────────────────────────────────

func TestStockPlanStep_ClipsURLOnly_NormalisesToClipDuration(t *testing.T) {
	// URL-only clip: Start=0, End=0, ClipDuration=10.
	// Expected: EndSec normalised to 10.
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test1"},
	}, 10, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].StartSec != 0 {
		t.Errorf("expected StartSec=0, got %v", plans[0].StartSec)
	}
	if plans[0].EndSec != 10 {
		t.Errorf("expected EndSec=10 (clip duration), got %v", plans[0].EndSec)
	}
}

func TestStockPlanStep_PlansEveryDirectURL(t *testing.T) {
	runner := newFakeRunner(nil, 4, "")
	runner.runInput.DirectURLs = []string{
		"https://www.youtube.com/watch?v=source-a",
		"https://www.youtube.com/watch?v=source-b",
	}
	runner.runInput.TotalMinutes = 1
	runner.cfg.ClipDurationSec = 4

	if err := (StockPlanStep{}).Run(context.Background(), runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 30 {
		t.Fatalf("expected 30 plans (15 per source), got %d", len(plans))
	}
	seen := map[string]int{}
	for _, plan := range plans {
		seen[plan.SourceID]++
	}
	if seen[runner.runInput.DirectURLs[0]] != 15 || seen[runner.runInput.DirectURLs[1]] != 15 {
		t.Fatalf("expected 15 plans per source, got %#v", seen)
	}
}

func TestStockPlanStep_ClipsURLOnly_ClipDurationZero_FallbackTo10(t *testing.T) {
	// URL-only clip with ClipDuration=0 (handler not set).
	// Expected: EndSec falls back to 10 (defensive default).
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test2"},
	}, 0, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].EndSec != 10 {
		t.Errorf("expected EndSec=10 (defensive fallback), got %v", plans[0].EndSec)
	}
}

func TestStockPlanStep_ClipsURLOnly_ClipDurationNegative_FallbackTo10(t *testing.T) {
	// URL-only clip with negative ClipDuration (-5).
	// Expected: EndSec falls back to 10 (defensive default).
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test3"},
	}, -5, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].EndSec != 10 {
		t.Errorf("expected EndSec=10 (defensive fallback for negative), got %v", plans[0].EndSec)
	}
}

func TestStockPlanStep_ClipsStartOnly_NormalisesEndToStartPlusClipDur(t *testing.T) {
	// Start-only clip: Start=5, End=0, ClipDuration=10.
	// Expected: EndSec = 5 + 10 = 15.
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test4", StartSec: 5},
	}, 10, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].StartSec != 5 {
		t.Errorf("expected StartSec=5, got %v", plans[0].StartSec)
	}
	if plans[0].EndSec != 15 {
		t.Errorf("expected EndSec=15 (5+10), got %v", plans[0].EndSec)
	}
}

func TestStockPlanStep_ClipsExplicitRange_PassesThroughUnchanged(t *testing.T) {
	// Explicit range: Start=5, End=30.
	// Expected: both values pass through unchanged.
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test5", StartSec: 5, EndSec: 30},
	}, 10, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].StartSec != 5 {
		t.Errorf("expected StartSec=5 (unchanged), got %v", plans[0].StartSec)
	}
	if plans[0].EndSec != 30 {
		t.Errorf("expected EndSec=30 (unchanged), got %v", plans[0].EndSec)
	}
}

func TestStockPlanStep_ClipsDescription_Preserved(t *testing.T) {
	runner := newFakeRunner([]ClipSpec{
		{
			URL:         "https://www.youtube.com/watch?v=test-desc",
			StartSec:    12,
			EndSec:      18,
			Title:       "Round 3",
			Description: "Pacquiao pressures Broner with a sharp left hand and body work.",
		},
	}, 6, "")
	step := StockPlanStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].Description != "Pacquiao pressures Broner with a sharp left hand and body work." {
		t.Fatalf("expected description to be preserved, got %q", plans[0].Description)
	}
}

func TestStockPlanStep_ClipsMixed_NormalisedAndExplicit(t *testing.T) {
	// Mixed batch: URL-only + start-only + explicit range.
	// ClipDuration=15.
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=a"},                           // → End=15
		{URL: "https://www.youtube.com/watch?v=b", StartSec: 3},              // → End=18
		{URL: "https://www.youtube.com/watch?v=c", StartSec: 10, EndSec: 60}, // unchanged
	}, 15, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(plans))
	}

	// Clip 0: URL-only → End=15.
	if plans[0].StartSec != 0 || plans[0].EndSec != 15 {
		t.Errorf("clip[0]: expected (0,15), got (%v,%v)", plans[0].StartSec, plans[0].EndSec)
	}
	// Clip 1: start-only (3) → End=3+15=18.
	if plans[1].StartSec != 3 || plans[1].EndSec != 18 {
		t.Errorf("clip[1]: expected (3,18), got (%v,%v)", plans[1].StartSec, plans[1].EndSec)
	}
	// Clip 2: explicit (10,60) → unchanged.
	if plans[2].StartSec != 10 || plans[2].EndSec != 60 {
		t.Errorf("clip[2]: expected (10,60), got (%v,%v)", plans[2].StartSec, plans[2].EndSec)
	}
}

func TestStockPlanStep_ClipsStartOnlyDefaultEnd_RoundedCorrectly(t *testing.T) {
	// Start=7.5 (float), ClipDuration=10 → End = 17.5.
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test6", StartSec: 7.5},
	}, 10, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].StartSec != 7.5 {
		t.Errorf("expected StartSec=7.5, got %v", plans[0].StartSec)
	}
	if plans[0].EndSec != 17.5 {
		t.Errorf("expected EndSec=17.5, got %v", plans[0].EndSec)
	}
}

func TestStockPlanStep_ClipsEndSecZeroFloat_RoundedCorrectly(t *testing.T) {
	// Start=0 (float zero), End=0 (float zero), ClipDuration=7.
	// The zero check is `clip.EndSec == 0` which matches `float64(0)`.
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test7", StartSec: 0, EndSec: 0},
	}, 7, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	if plans[0].EndSec != 7 {
		t.Errorf("expected EndSec=7, got %v", plans[0].EndSec)
	}
}

func TestStockPlanStep_ExplicitClips_ExpandSecondsPerSegment(t *testing.T) {
	runner := newFakeRunner([]ClipSpec{
		{
			URL:         "https://www.youtube.com/watch?v=test-expand",
			Title:       "Round 7",
			Description: "Short test clip",
			StartSec:    32,
			EndSec:      42,
			Slug:        "segment-07-il-finale-del-match",
		},
	}, 10, "")
	runner.runInput.SecondsPerSegment = 5

	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	plans := runner.State().Plan
	if len(plans) != 2 {
		t.Fatalf("expected 2 expanded plans, got %d", len(plans))
	}
	if plans[0].StartSec != 32 || plans[0].EndSec != 37 {
		t.Fatalf("first plan range = [%v,%v], want [32,37]", plans[0].StartSec, plans[0].EndSec)
	}
	if plans[1].StartSec != 37 || plans[1].EndSec != 42 {
		t.Fatalf("second plan range = [%v,%v], want [37,42]", plans[1].StartSec, plans[1].EndSec)
	}
	if plans[0].Slug != "segment-07-il-finale-del-match-0-0-32_to_0-0-37" {
		t.Fatalf("first plan slug = %q, want timestamp-suffixed slug", plans[0].Slug)
	}
	if plans[1].Slug != "segment-07-il-finale-del-match-0-0-37_to_0-0-42" {
		t.Fatalf("second plan slug = %q, want timestamp-suffixed slug", plans[1].Slug)
	}
}

func TestStockPlanStep_ExplicitClips_LongClip_DefaultsToFiveSecondSegments(t *testing.T) {
	runner := newFakeRunner([]ClipSpec{
		{
			URL:      "https://www.youtube.com/watch?v=test-auto-expand",
			Title:    "Round 1",
			StartSec: 0,
			EndSec:   60,
			Slug:     "pacquiao-boner-round-1",
		},
	}, 10, "")

	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	plans := runner.State().Plan
	if len(plans) != 12 {
		t.Fatalf("expected 12 auto-expanded plans, got %d", len(plans))
	}
	for i, plan := range plans {
		wantStart := float64(i * 5)
		wantEnd := wantStart + 5
		if plan.StartSec != wantStart || plan.EndSec != wantEnd {
			t.Fatalf("plan[%d] range = [%v,%v], want [%v,%v]", i, plan.StartSec, plan.EndSec, wantStart, wantEnd)
		}
	}
	if plans[0].Slug != "pacquiao-boner-round-1-0-0-0_to_0-0-5" {
		t.Fatalf("first auto-expanded slug = %q, want timestamp-suffixed slug", plans[0].Slug)
	}
	if plans[11].Slug != "pacquiao-boner-round-1-0-0-55_to_0-1-0" {
		t.Fatalf("last auto-expanded slug = %q, want timestamp-suffixed slug", plans[11].Slug)
	}
}

func TestStockPlanStep_ExplicitClips_SubSixtySecondsStaySingle(t *testing.T) {
	runner := newFakeRunner([]ClipSpec{
		{
			URL:      "https://www.youtube.com/watch?v=test-no-expand",
			Title:    "Round 2",
			StartSec: 0,
			EndSec:   59,
			Slug:     "pacquiao-boner-round-2",
		},
	}, 10, "")

	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan for sub-60s clip, got %d", len(plans))
	}
	if plans[0].StartSec != 0 || plans[0].EndSec != 59 {
		t.Fatalf("sub-60s plan range = [%v,%v], want [0,59]", plans[0].StartSec, plans[0].EndSec)
	}
}

// ── Error-path tests ────────────────────────────────────────────────

func TestStockPlanStep_ClipsNoURL_ReturnsError(t *testing.T) {
	// All clips have empty URLs.
	runner := newFakeRunner([]ClipSpec{
		{StartSec: 0, EndSec: 0},
		{StartSec: 5, EndSec: 30},
	}, 10, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err == nil {
		t.Fatalf("expected error for clips with no URL")
	}
	if !strings.Contains(err.Error(), "explicit clips require either a per-clip URL or a root source") {
		t.Errorf("expected URL-required error, got: %v", err)
	}
}

func TestStockPlanStep_ClipsEmptyNoURL_ReturnsError(t *testing.T) {
	// Clips present but URL is empty string (explicit empty).
	runner := newFakeRunner([]ClipSpec{
		{URL: "", StartSec: 10, EndSec: 30},
	}, 10, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err == nil {
		t.Fatalf("expected error for clips with empty URL")
	}
	if !strings.Contains(err.Error(), "explicit clips require either a per-clip URL or a root source") {
		t.Errorf("expected URL-required error, got: %v", err)
	}
}

func TestStockPlanStep_ClipsSourceIDIsClipURL(t *testing.T) {
	// The VideoSource.SourceID comes from the clip URL (the explicit
	// planner uses src.URL for SourceID). Verify it propagates correctly.
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test8", Title: "My Custom Title"},
	}, 10, "")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 1 {
		t.Fatalf("expected 1 plan, got %d", len(plans))
	}
	// Title flows into the VideoSource and through to the plan's SourceID.
	if plans[0].SourceID != "https://www.youtube.com/watch?v=test8" {
		t.Errorf("expected SourceID to be the clip URL, got %q", plans[0].SourceID)
	}
}

// ── Non-clips path: deterministic planner still works ──────────────

func TestStockPlanStep_NoClips_UsesDeterministicPlanner(t *testing.T) {
	// When no clips are provided, the step should fall through to
	// the deterministic planner with DirectURLs.
	runner := &fakeStepRunner{
		runInput: &RunInput{
			DirectURLs:   []string{"https://www.youtube.com/watch?v=direct"},
			TotalMinutes: 1,
		},
		cfg: OrchestratorConfig{
			PolicyVersion:    "test-policy-v1",
			ClipDurationSec:  10,
			ChunkDurationSec: 60,
		},
		state:   &RunState{},
		planner: NewDeterministicPlanner(),
	}
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) == 0 {
		t.Fatalf("expected non-empty plan from deterministic planner")
	}
	// Deterministic planner with TotalMinutes=1, ClipDurationSec=10
	// should produce 6 plans (60s budget / 10s clip = 6).
	if len(plans) != 6 {
		t.Errorf("expected 6 plans (60s/10s), got %d", len(plans))
	}
}

// ── Policy version propagation ─────────────────────────────────────

func TestStockPlanStep_ClipsPropagatePolicyVersion(t *testing.T) {
	// The PolicyVersion from Cfg() should appear on every plan.
	runner := newFakeRunner([]ClipSpec{
		{URL: "https://www.youtube.com/watch?v=test9"},
	}, 10, "canary-policy-v2")
	step := StockPlanStep{}
	err := step.Run(context.Background(), runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, plan := range runner.State().Plan {
		if plan.PolicyVersion != "canary-policy-v2" {
			t.Errorf("plan[%d].PolicyVersion: expected canary-policy-v2, got %q", i, plan.PolicyVersion)
		}
	}
}

// TestStockPlanStep_ShortSourceUsesRealDuration guards against the
// Muhammad Ali 10m E2E regression (2026-08-07): resolveInputQueries
// discarded the provider-known source duration, so concreteSources
// passed DurationSec=0 to the deterministic planner, which fell back
// to the budget*10 horizon (600s) and planned a clip ending at 310s
// for a 226s source → ErrStockClipsOutOfRange failed the whole run.
// With SourceDurations populated, every planned clip must stay within
// the real source length.
func TestStockPlanStep_ShortSourceUsesRealDuration(t *testing.T) {
	const shortURL = "https://www.youtube.com/watch?v=short226"
	runner := &fakeStepRunner{
		runInput: &RunInput{
			DirectURLs:                     []string{shortURL},
			SourceDurations:                map[string]float64{shortURL: 226.19},
			TargetDurationPerSourceSeconds: 60,
			ClipsPerSource:                 3,
			ClipDurationSeconds:            20,
		},
		cfg: OrchestratorConfig{
			PolicyVersion:    "test-policy-v1",
			ClipDurationSec:  20,
			ChunkDurationSec: 60,
		},
		state:   &RunState{},
		planner: NewDeterministicPlanner(),
	}
	step := StockPlanStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans (60s/20s), got %d", len(plans))
	}
	for i, plan := range plans {
		if plan.EndSec > 226.19 {
			t.Errorf("plan[%d] EndSec=%.2f exceeds real source duration 226.19s (would fail with ErrStockClipsOutOfRange)", i, plan.EndSec)
		}
		if plan.StartSec < 0 || plan.EndSec <= plan.StartSec {
			t.Errorf("plan[%d] invalid range [%v,%v]", i, plan.StartSec, plan.EndSec)
		}
	}
}

// TestStockPlanStep_MetadataRoundingAbsorbedByMargin guards against the
// second Muhammad Ali 10m E2E regression (2026-08-07): YouTube declared
// a source duration of 902s but the downloaded file measures 901.89s
// (0.11s metadata rounding). The planner must subtract its safety margin
// so the last clip's EndSec stays within the REAL probed duration.
func TestStockPlanStep_MetadataRoundingAbsorbedByMargin(t *testing.T) {
	const url = "https://www.youtube.com/watch?v=rounding902"
	runner := &fakeStepRunner{
		runInput: &RunInput{
			DirectURLs:                     []string{url},
			SourceDurations:                map[string]float64{url: 902.0}, // declared by provider
			TargetDurationPerSourceSeconds: 60,
			ClipsPerSource:                 3,
			ClipDurationSeconds:            20,
		},
		cfg: OrchestratorConfig{
			PolicyVersion:    "test-policy-v1",
			ClipDurationSec:  20,
			ChunkDurationSec: 60,
		},
		state:   &RunState{},
		planner: NewDeterministicPlanner(),
	}
	step := StockPlanStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	plans := runner.State().Plan
	if len(plans) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(plans))
	}
	// Real probed duration is 901.89 — every clip must end before it
	// even though the declared duration was 902.0.
	const realDuration = 901.89
	for i, plan := range plans {
		if plan.EndSec > realDuration {
			t.Errorf("plan[%d] EndSec=%.2f exceeds real duration %.2f (metadata rounding must be absorbed by margin)", i, plan.EndSec, realDuration)
		}
	}
}

// TestStockPlanStep_UnknownDurationKeepsFallbackContract verifies that
// sources without a known duration keep the previous behavior: the
// planner uses its conservative horizon and the plan is produced
// without error (extract-time bounds check remains the fail-closed
// guard).
func TestStockPlanStep_UnknownDurationKeepsFallbackContract(t *testing.T) {
	runner := &fakeStepRunner{
		runInput: &RunInput{
			DirectURLs:                     []string{"https://www.youtube.com/watch?v=unknown"},
			TargetDurationPerSourceSeconds: 60,
			ClipsPerSource:                 3,
			ClipDurationSeconds:            20,
		},
		cfg: OrchestratorConfig{
			PolicyVersion:    "test-policy-v1",
			ClipDurationSec:  20,
			ChunkDurationSec: 60,
		},
		state:   &RunState{},
		planner: NewDeterministicPlanner(),
	}
	step := StockPlanStep{}
	if err := step.Run(context.Background(), runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(runner.State().Plan); got != 3 {
		t.Fatalf("expected 3 plans, got %d", got)
	}
}

// ── Compile-time guard: fakeStepRunner satisfies StepRunner ────────

var _ StepRunner = (*fakeStepRunner)(nil)

// avoid unused import warning for job package (referenced by RunState.Manifest type).
var _ job.Status
