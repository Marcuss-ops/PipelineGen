package cliprender

import (
	"errors"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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

// TestCompile_SealsSinglePassOverlay verifies the resolved overlay segment is
// sealed into the plan with its exact window and lineage, so Chronon
// composites it in the SAME render pass (one encode) instead of a post-render
// compositing pass.
func TestCompile_SealsSinglePassOverlay(t *testing.T) {
	in := baseCompileInput()
	in.DurationMS = 4000
	in.Overlay = &PlanOverlayInput{
		Segments: []PlanOverlayInputSegment{{
			Segment: &OverlaySegment{
				RenderJobID: "render-overlay-001",
				RenderKey:   "rk-overlay-001",
				LocalPath:   "/scratch/overlay-segment.mp4",
				SHA256:      strings.Repeat("b", 64),
				SizeBytes:   2048,
			},
			StartMS: 1000,
			EndMS:   3000,
		}},
	}
	plan, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if plan.Overlay == nil || len(plan.Overlay.Segments) != 1 {
		t.Fatalf("sealed plan must carry exactly the declared overlay segment, got %+v", plan.Overlay)
	}
	seg := plan.Overlay.Segments[0]
	if seg.RenderJobID != "render-overlay-001" || seg.RenderKey != "rk-overlay-001" {
		t.Fatalf("overlay lineage = %+v", seg)
	}
	if seg.Path != "/scratch/overlay-segment.mp4" || seg.SHA256 != strings.Repeat("b", 64) {
		t.Fatalf("overlay segment = %+v", seg)
	}
	if seg.StartMS != 1000 || seg.EndMS != 3000 {
		t.Fatalf("overlay window = [%d, %d)ms, want [1000, 3000)", seg.StartMS, seg.EndMS)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate failed on a sealed overlay plan: %v", err)
	}
	// The overlay is part of the sealed identity: a different window changes
	// the plan digest (a rerun can never silently reuse a stale plan).
	other := baseCompileInput()
	other.DurationMS = 4000
	other.Overlay = &PlanOverlayInput{
		Segments: []PlanOverlayInputSegment{{Segment: in.Overlay.Segments[0].Segment, StartMS: 1000, EndMS: 2500}},
	}
	otherPlan, err := Compile(other)
	if err != nil {
		t.Fatalf("Compile(other) failed: %v", err)
	}
	if otherPlan.PlanSHA256 == plan.PlanSHA256 {
		t.Fatal("a different overlay window must change the sealed plan digest")
	}
}

// TestCompile_SealsEveryOverlaySegment pins the contract that makes a
// multi-item scene expressible: a clip that composites N certified overlay
// artifacts seals ALL N segments with their own lineage and window. Modelled as
// a single slot this silently dropped every item after the first.
func TestCompile_SealsEveryOverlaySegment(t *testing.T) {
	segment := func(job, key, sha string) *OverlaySegment {
		return &OverlaySegment{RenderJobID: job, RenderKey: key, LocalPath: "/scratch/" + key + ".mp4", SHA256: sha}
	}
	in := baseCompileInput()
	in.DurationMS = 8000
	in.Overlay = &PlanOverlayInput{Segments: []PlanOverlayInputSegment{
		{Segment: segment("job-phrase", "rk-phrase", strings.Repeat("b", 64)), StartMS: 500, EndMS: 2500},
		{Segment: segment("job-entity", "rk-entity", strings.Repeat("c", 64)), StartMS: 3000, EndMS: 6000},
		{Segment: segment("job-keyword", "rk-keyword", strings.Repeat("d", 64)), StartMS: 6500, EndMS: 7800},
	}}
	plan, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if plan.Overlay == nil || len(plan.Overlay.Segments) != 3 {
		t.Fatalf("sealed plan must carry all 3 declared segments, got %+v", plan.Overlay)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate failed on a multi-segment overlay plan: %v", err)
	}
	for i, want := range []struct {
		key            string
		sha            string
		startMS, endMS int64
	}{
		{"rk-phrase", strings.Repeat("b", 64), 500, 2500},
		{"rk-entity", strings.Repeat("c", 64), 3000, 6000},
		{"rk-keyword", strings.Repeat("d", 64), 6500, 7800},
	} {
		got := plan.Overlay.Segments[i]
		if got.RenderKey != want.key || got.SHA256 != want.sha || got.StartMS != want.startMS || got.EndMS != want.endMS {
			t.Fatalf("segment %d = %+v, want key=%s sha=%s window=[%d,%d)", i, got, want.key, want.sha[:8], want.startMS, want.endMS)
		}
	}

	// Dropping one segment must change the sealed digest: a plan that lost an
	// item can never be mistaken for the cached full-overlay plan.
	partial := baseCompileInput()
	partial.DurationMS = 8000
	partial.Overlay = &PlanOverlayInput{Segments: in.Overlay.Segments[:2]}
	partialPlan, err := Compile(partial)
	if err != nil {
		t.Fatalf("Compile(partial) failed: %v", err)
	}
	if partialPlan.PlanSHA256 == plan.PlanSHA256 {
		t.Fatal("a plan missing a declared overlay segment must not share the full plan digest")
	}

	// The last declared segment is still exactly where the caller put it: the
	// sealed order is the declared order (so provenance is positional).
	if plan.Overlay.Segments[2].RenderKey != in.Overlay.Segments[2].Segment.RenderKey {
		t.Fatal("declared segment order must be preserved")
	}
}

// TestCompile_OverlayFailClosed pins the overlay validation rules.
func TestCompile_OverlayFailClosed(t *testing.T) {
	seg := func() *OverlaySegment {
		return &OverlaySegment{LocalPath: "/scratch/x.mp4", SHA256: strings.Repeat("b", 64)}
	}
	segment := func(startMS, endMS int64) []PlanOverlayInputSegment {
		return []PlanOverlayInputSegment{{Segment: seg(), StartMS: startMS, EndMS: endMS}}
	}
	cases := []struct {
		name    string
		overlay *PlanOverlayInput
	}{
		// A declared overlay with no segment composites nothing: the single
		// worst outcome, so it is rejected before any process starts.
		{"no segments", &PlanOverlayInput{}},
		{"missing segment", &PlanOverlayInput{Segments: []PlanOverlayInputSegment{{StartMS: 0, EndMS: 1000}}}},
		{"segment without sha256", &PlanOverlayInput{Segments: []PlanOverlayInputSegment{{Segment: &OverlaySegment{LocalPath: "/scratch/x.mp4"}, EndMS: 1000}}}},
		{"empty window", &PlanOverlayInput{Segments: segment(1000, 1000)}},
		{"negative start", &PlanOverlayInput{Segments: segment(-1, 1000)}},
		{"window past clip duration", &PlanOverlayInput{Segments: segment(0, 9000)}},
		// One bad segment among good ones rejects the whole plan: the clip is
		// never composited with a partial overlay set.
		{"one bad segment among good ones", &PlanOverlayInput{Segments: []PlanOverlayInputSegment{
			{Segment: seg(), StartMS: 0, EndMS: 1000},
			{Segment: &OverlaySegment{LocalPath: "/scratch/x.mp4"}, StartMS: 1000, EndMS: 2000},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseCompileInput()
			in.DurationMS = 4000
			in.Overlay = tc.overlay
			if _, err := Compile(in); err == nil {
				t.Fatal("invalid overlay must fail closed")
			}
		})
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
	in.BackgroundKind = BackgroundKindVideo
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
		plan.Background.AssetID != "asset-bg" || plan.Background.Kind != BackgroundKindVideo {
		t.Errorf("background: got %+v", plan.Background)
	}
	if plan.Subtitles == nil || plan.Subtitles.Mode != SubtitlesModeBurn ||
		plan.Subtitles.StyleID != "shorts-v1" ||
		plan.Subtitles.Path != "/scratch/run-1/subtitles.ass" {
		t.Errorf("subtitles: got %+v", plan.Subtitles)
	}
}

// TestCompile_PropagatesVisualStyle verifies the canonical style blocks
// (watermark + subtitles) reach the sealed plan without loss — the exact
// script.generate payload shape projected verbatim.
func TestCompile_PropagatesVisualStyle(t *testing.T) {
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
		Opacity:  0.9,
		MarginPX: 24,
		Style: &scriptpkg.VideoVisualStyleSpec{
			WidthPX:      180,
			ScalePercent: 100,
			Shadow: &scriptpkg.VideoShadowSpec{
				Color:   "#000000",
				Opacity: 0.55,
				BlurPX:  14,
				OffsetY: 8,
			},
			TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 250},
		},
	}
	in.Subtitles = &SubtitleArtifact{
		LocalPath: "/scratch/run-1/subtitles.ass",
		SHA256:    "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		Mode:      SubtitlesModeBurn,
		StyleID:   "shorts-v1",
	}
	in.SubtitlesStyle = &scriptpkg.VideoVisualStyleSpec{
		Color:      "#FFFFFF",
		FontSizePX: 54,
		Shadow: &scriptpkg.VideoShadowSpec{
			Color:   "#000000",
			Opacity: 0.7,
			BlurPX:  10,
			OffsetY: 5,
		},
		TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 120},
	}

	plan, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if plan.Watermark == nil || plan.Watermark.Style == nil {
		t.Fatalf("watermark style lost: %+v", plan.Watermark)
	}
	wm := plan.Watermark.Style
	if wm.WidthPX != 180 || wm.ScalePercent != 100 {
		t.Errorf("watermark size lost: %+v", wm)
	}
	if wm.Shadow == nil || wm.Shadow.Color != "#000000" || wm.Shadow.Opacity != 0.55 || wm.Shadow.BlurPX != 14 || wm.Shadow.OffsetY != 8 {
		t.Errorf("watermark shadow lost: %+v", wm.Shadow)
	}
	if wm.TransitionIn == nil || wm.TransitionIn.Preset != "fade_in" || wm.TransitionIn.DurationMS != 250 {
		t.Errorf("watermark transition lost: %+v", wm.TransitionIn)
	}
	if plan.Subtitles == nil || plan.Subtitles.Style == nil {
		t.Fatalf("subtitle style lost: %+v", plan.Subtitles)
	}
	sub := plan.Subtitles.Style
	if sub.Color != "#FFFFFF" || sub.FontSizePX != 54 {
		t.Errorf("subtitle style lost: %+v", sub)
	}
	if sub.Shadow == nil || sub.Shadow.Opacity != 0.7 || sub.Shadow.BlurPX != 10 || sub.Shadow.OffsetY != 5 {
		t.Errorf("subtitle shadow lost: %+v", sub.Shadow)
	}
	if sub.TransitionIn == nil || sub.TransitionIn.Preset != "fade_in" || sub.TransitionIn.DurationMS != 120 {
		t.Errorf("subtitle transition lost: %+v", sub.TransitionIn)
	}

	// The sealed plan must still validate (style folds into PlanSHA256).
	if err := plan.Validate(); err != nil {
		t.Fatalf("sealed plan with style must validate: %v", err)
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

// TestCompile_ForegroundScaleGatesBackgroundVisibility pins the ONE knob that
// decides whether an asset background is actually VISIBLE: foreground_scale_percent.
//
// ClipRenderPlanV1 treats 0 and 100 identically ("full canvas"), so a request
// that asks for background.mode=asset and leaves output.foreground_scale_percent
// unset normalises to 100, and the source covers the entire canvas: the plate is
// materialised, hashed, sealed into the plan and composited UNDER an opaque
// foreground — paid for and invisible. It is a silent no-op exactly where the
// caller asked for a background, which is why every canonical fixture
// (ops/jobs/*.generate.json, ops/benchmarks/clip-render-background-payload.json)
// sets 80. This test makes the requirement executable instead of a convention.
func TestCompile_ForegroundScaleGatesBackgroundVisibility(t *testing.T) {
	// Unset and an explicit 100 are the same contract: the plate is sealed but
	// can never be seen. Both must be reported honestly as 100 on the plan so a
	// post-mortem can tell "hidden by construction" from "not resolved".
	for _, scale := range []int{0, 100} {
		in := baseCompileInput()
		in.Background = backgroundMat()
		in.BackgroundMode = BackgroundModeAsset
		in.BackgroundKind = BackgroundKindVideo
		in.ForegroundScalePercent = scale

		plan, err := Compile(in)
		if err != nil {
			t.Fatalf("Compile(scale=%d): %v", scale, err)
		}
		if got := plan.Output.ForegroundScalePercent; got != 100 {
			t.Fatalf("scale=%d sealed as %d, want 100 (full canvas hides the plate)", scale, got)
		}
		if plan.Background == nil || plan.Background.Mode != BackgroundModeAsset || plan.Background.AssetID != "asset-bg" {
			t.Fatalf("scale=%d must still seal the resolved background: %+v", scale, plan.Background)
		}
	}

	// A real scale-down keeps the plate visible and survives sealing verbatim.
	visible := baseCompileInput()
	visible.Background = backgroundMat()
	visible.BackgroundMode = BackgroundModeAsset
	visible.BackgroundKind = BackgroundKindVideo
	visible.ForegroundScalePercent = 80
	plan, err := Compile(visible)
	if err != nil {
		t.Fatalf("Compile(scale=80): %v", err)
	}
	if plan.Output.ForegroundScalePercent != 80 {
		t.Fatalf("scale 80 must survive sealing, got %d", plan.Output.ForegroundScalePercent)
	}

	// The request boundary rejects an out-of-range scale rather than clamping it
	// into a different render than the caller asked for.
	req := baseRenderRequest()
	req.Output.ForegroundScalePercent = 101
	if err := req.Validate(); err == nil {
		t.Fatal("output.foreground_scale_percent=101 must fail validation")
	} else if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error %v is not ErrInvalidRequest", err)
	}

	// ...and Compile itself fails closed on an out-of-range scale, so no plan
	// carrying one can ever reach a renderer (no silent clamp to a different
	// composition than the caller asked for).
	over := baseCompileInput()
	over.Background = backgroundMat()
	over.BackgroundMode = BackgroundModeAsset
	over.BackgroundKind = BackgroundKindVideo
	over.ForegroundScalePercent = 101
	if _, err := Compile(over); !errors.Is(err, ErrInvalidClipPlan) {
		t.Fatalf("Compile(scale=101) must fail closed with ErrInvalidClipPlan, got %v", err)
	}
}
