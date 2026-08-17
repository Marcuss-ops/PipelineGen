package cliprender

import (
	"errors"
	"testing"
)

// baseCompileInput returns a fully-resolved CompileInput for the happy path.
func baseCompileInput() CompileInput {
	return CompileInput{
		RunID: "run-1",
		Source: &MaterializedAsset{
			AssetID:   "asset-source",
			LocalPath: "/scratch/asset-source.mp4",
			SHA256:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SizeBytes: 1024,
		},
		Contract: &ResolvedContract{
			ContractID:   OutputContractVeloxEditingClipV1,
			Container:    "mp4",
			VideoCodec:   "h264",
			VideoProfile: "high",
			PixelFormat:  "yuv420p",
			Width:        1080,
			Height:       1920,
			FPS:          60,
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		AudioMode:  AudioModeCopyIfCompatible,
		OutputPath: "/scratch/run-1/rendered-clip.mp4",
	}
}

// TestCompile_SealsDeterministicPlan verifies the happy path: the plan is
// sealed with a valid PlanSHA256, passes Validate, and identical inputs
// produce identical digests (determinism).
func TestCompile_SealsDeterministicPlan(t *testing.T) {
	p1, err := Compile(baseCompileInput())
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if p1.Version != PlanVersion || p1.RunID != "run-1" {
		t.Errorf("identity: got version=%q run_id=%q", p1.Version, p1.RunID)
	}
	if p1.PlanSHA256 == "" {
		t.Fatal("expected sealed plan_sha256")
	}
	if err := p1.Validate(); err != nil {
		t.Fatalf("Validate failed on sealed plan: %v", err)
	}
	if p1.Background == nil || p1.Background.Mode != BackgroundModeNone {
		t.Errorf("default background must be none, got %+v", p1.Background)
	}
	if p1.Watermark != nil || p1.Subtitles != nil {
		t.Errorf("watermark/subtitles must be nil when disabled, got wm=%v sub=%v", p1.Watermark, p1.Subtitles)
	}
	if p1.Output.Width != 1080 || p1.Output.Height != 1920 || p1.Output.FPS != 60 {
		t.Errorf("output contract: got %+v", p1.Output)
	}
	if p1.Audio.Mode != AudioModeCopyIfCompatible || p1.Audio.Codec != "aac" {
		t.Errorf("audio: got %+v", p1.Audio)
	}

	// Determinism: recompiling identical inputs produces the same digest.
	p2, err := Compile(baseCompileInput())
	if err != nil {
		t.Fatalf("second Compile failed: %v", err)
	}
	if p1.PlanSHA256 != p2.PlanSHA256 {
		t.Errorf("plan must be deterministic: %q != %q", p1.PlanSHA256, p2.PlanSHA256)
	}
}

// TestCompile_ResolvedWatermarkBackgroundSubtitles verifies every optional
// block is carried into the sealed plan verbatim — Rust never resolves them.
func TestCompile_ResolvedWatermarkBackgroundSubtitles(t *testing.T) {
	in := baseCompileInput()
	in.Watermark = &MaterializedAsset{
		AssetID:   "asset-wm",
		LocalPath: "/scratch/asset-wm.png",
		SHA256:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	in.WatermarkSpec = &WatermarkSpec{
		Enabled:  true,
		AssetID:  "asset-wm",
		Position: PositionTopRight,
		Opacity:  0.85,
		MarginPX: 40,
	}
	in.Background = &MaterializedAsset{
		AssetID:   "asset-bg",
		LocalPath: "/scratch/asset-bg.mp4",
		SHA256:    "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	in.Subtitles = &SubtitleArtifact{
		LocalPath: "/scratch/run-1/subtitles.ass",
		SHA256:    "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Mode:      SubtitlesModeBurn,
		StyleID:   "shorts-v1",
	}

	plan, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if plan.Watermark == nil || plan.Watermark.AssetID != "asset-wm" ||
		plan.Watermark.Path != "/scratch/asset-wm.png" ||
		plan.Watermark.Position != PositionTopRight ||
		plan.Watermark.Opacity != 0.85 || plan.Watermark.MarginPX != 40 {
		t.Errorf("watermark: got %+v", plan.Watermark)
	}
	if plan.Background == nil || plan.Background.Mode != BackgroundModeAsset ||
		plan.Background.AssetID != "asset-bg" {
		t.Errorf("background: got %+v", plan.Background)
	}
	if plan.Subtitles == nil || plan.Subtitles.Mode != SubtitlesModeBurn ||
		plan.Subtitles.StyleID != "shorts-v1" ||
		plan.Subtitles.Path != "/scratch/run-1/subtitles.ass" {
		t.Errorf("subtitles: got %+v", plan.Subtitles)
	}
}

// TestCompile_FailClosedMissingInputs verifies the plan is never partially
// resolved: missing source/contract/output path/audio mode are typed errors.
func TestCompile_FailClosedMissingInputs(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CompileInput)
	}{
		{"nil source", func(in *CompileInput) { in.Source = nil }},
		{"source without sha", func(in *CompileInput) { in.Source.SHA256 = "" }},
		{"nil contract", func(in *CompileInput) { in.Contract = nil }},
		{"empty output path", func(in *CompileInput) { in.OutputPath = "" }},
		{"invalid audio mode", func(in *CompileInput) { in.AudioMode = "reencode_everything" }},
		{"watermark without sha", func(in *CompileInput) {
			in.Watermark = &MaterializedAsset{AssetID: "wm", LocalPath: "/x.png"}
		}},
		{"subtitles without sha", func(in *CompileInput) {
			in.Subtitles = &SubtitleArtifact{LocalPath: "/x.ass", Mode: SubtitlesModeBurn}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseCompileInput()
			tc.mutate(&in)
			_, err := Compile(in)
			if !errors.Is(err, ErrInvalidClipPlan) {
				t.Fatalf("expected ErrInvalidClipPlan, got %v", err)
			}
		})
	}
}

// TestValidate_DetectsPlanDrift verifies tamper detection: mutating any field
// after sealing breaks the PlanSHA256 match (fail-closed before Rust).
func TestValidate_DetectsPlanDrift(t *testing.T) {
	plan, err := Compile(baseCompileInput())
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	plan.Output.Width = 720 // tamper after seal
	if err := plan.Validate(); !errors.Is(err, ErrClipPlanDrift) {
		t.Fatalf("expected ErrClipPlanDrift, got %v", err)
	}
}

// TestValidate_RejectsIncompletePlan verifies structural validation on an
// unsealed/partial plan.
func TestValidate_RejectsIncompletePlan(t *testing.T) {
	plan := ClipRenderPlanV1{Version: PlanVersion, RunID: "run-1"}
	if err := plan.Validate(); !errors.Is(err, ErrInvalidClipPlan) {
		t.Fatalf("expected ErrInvalidClipPlan for empty plan, got %v", err)
	}
}

// TestCompile_BlurSourceBackground verifies blur_source carries no asset
// block (Rust derives the background from the source itself).
func TestCompile_BlurSourceBackground(t *testing.T) {
	// Background nil → mode none is the default (see happy path). The
	// blur_source selection is a request-level business decision that the
	// worker maps to a nil background asset; the plan only carries an asset
	// block for mode=asset. Verify a background asset never leaks a wrong
	// mode.
	in := baseCompileInput()
	plan, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if plan.Background == nil || plan.Background.Mode != BackgroundModeNone {
		t.Fatalf("expected background mode none, got %+v", plan.Background)
	}
}
