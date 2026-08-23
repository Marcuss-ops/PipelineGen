package cliprender

import (
	"errors"
	"strings"
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
			ContractID:         OutputContractVeloxAssemblyReadyV1,
			Container:          "mp4",
			VideoCodec:         "h264",
			VideoProfile:       "high",
			PixelFormat:        "yuv420p",
			Width:              1920,
			Height:             1080,
			FPSNum:             24,
			FPSDen:             1,
			KeyframeInterval:   48,
			AudioCodec:         "aac",
			AudioProfile:       "LC",
			SampleRate:         48000,
			Channels:           2,
			AudioChannelLayout: "stereo",
			AudioBitrate:       "128k",
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
	if p1.Output.Width != 1920 || p1.Output.Height != 1080 || p1.Output.FPSNum != 24 || p1.Output.FPSDen != 1 {
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

// TestCompile_BlurSourceBackground verifies blur_source is carried into the
// sealed plan WITHOUT an asset block (Rust derives the blurred background
// from the source itself) — the request-level mode is a business selection
// the worker passes through, never lost to mode=none.
func TestCompile_BlurSourceBackground(t *testing.T) {
	in := baseCompileInput()
	in.BackgroundMode = BackgroundModeBlurSource
	plan, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if plan.Background == nil || plan.Background.Mode != BackgroundModeBlurSource {
		t.Fatalf("expected background mode blur_source, got %+v", plan.Background)
	}
	if plan.Background.Path != "" || plan.Background.SHA256 != "" {
		t.Fatalf("blur_source must not carry an asset block, got %+v", plan.Background)
	}

	// Contradictory inputs fail closed: blur_source/none with an asset.
	bad := baseCompileInput()
	bad.BackgroundMode = BackgroundModeBlurSource
	bad.Background = &MaterializedAsset{AssetID: "bg", LocalPath: "/x.mp4", SHA256: strings.Repeat("a", 64)}
	if _, err := Compile(bad); err == nil {
		t.Fatal("blur_source with an asset must fail closed")
	}
	bad = baseCompileInput()
	bad.BackgroundMode = BackgroundModeNone
	bad.Background = &MaterializedAsset{AssetID: "bg", LocalPath: "/x.mp4", SHA256: strings.Repeat("a", 64)}
	if _, err := Compile(bad); err == nil {
		t.Fatal("none with an asset must fail closed")
	}
}
