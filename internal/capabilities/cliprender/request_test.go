package cliprender

import (
	"errors"
	"testing"
)

// TestNormalize_AppliesCanonicalDefaults verifies the idempotent
// default pass: an empty request gets every canonical default.
func TestNormalize_AppliesCanonicalDefaults(t *testing.T) {
	req := &RenderRequest{}
	req.Normalize()

	if req.Background.Mode != BackgroundModeNone {
		t.Errorf("Background.Mode: got %q, want none", req.Background.Mode)
	}
	if req.Watermark.Enabled {
		t.Error("Watermark must default to disabled")
	}
	if req.Watermark.Position != PositionTopRight {
		t.Errorf("Watermark.Position default: got %q, want top_right", req.Watermark.Position)
	}
	if req.Watermark.Opacity != 1.0 {
		t.Errorf("Watermark.Opacity default: got %v, want 1.0", req.Watermark.Opacity)
	}
	if req.Transcript.Mode != TranscriptModeReuseOrGenerate {
		t.Errorf("Transcript.Mode default: got %q, want reuse_or_generate", req.Transcript.Mode)
	}
	if req.Transcript.Language != DefaultLanguage {
		t.Errorf("Transcript.Language default: got %q, want en", req.Transcript.Language)
	}
	if req.Subtitles.Enabled {
		t.Error("Subtitles must default to disabled")
	}
	if req.Subtitles.Mode != SubtitlesModeBurn {
		t.Errorf("Subtitles.Mode default: got %q, want burn", req.Subtitles.Mode)
	}
	if req.Output.Contract != OutputContractVeloxAssemblyReadyV1 {
		t.Errorf("Output.Contract default: got %q, want VELOX_ASSEMBLY_READY_V1", req.Output.Contract)
	}
	if req.Output.Width != DefaultWidth || req.Output.Height != DefaultHeight || req.Output.FPSNum != DefaultFPSNum || req.Output.FPSDen != DefaultFPSDen {
		t.Errorf("Output defaults: got %dx%d@%d/%d, want %dx%d@%d/%d", req.Output.Width, req.Output.Height, req.Output.FPSNum, req.Output.FPSDen, DefaultWidth, DefaultHeight, DefaultFPSNum, DefaultFPSDen)
	}
	if req.Audio.Mode != AudioModeCopyIfCompatible {
		t.Errorf("Audio.Mode default: got %q, want copy_if_compatible", req.Audio.Mode)
	}
	if req.Destination.DriveFolderID != DefaultDriveRootFolderID {
		t.Errorf("Destination.DriveFolderID default: got %q, want %q", req.Destination.DriveFolderID, DefaultDriveRootFolderID)
	}
}

// TestNormalize_IsIdempotent verifies a second Normalize pass is a
// no-op (byte-identical normalized request).
func TestNormalize_IsIdempotent(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "asset-1"}
	req.Normalize()
	first := *req

	req.Normalize()
	second := *req

	if first.Background.Mode != second.Background.Mode ||
		first.Watermark.Position != second.Watermark.Position ||
		first.Output.Width != second.Output.Width ||
		first.Destination.DriveFolderID != second.Destination.DriveFolderID {
		t.Fatalf("Normalize is not idempotent: %+v vs %+v", first, second)
	}
}

// TestValidate_RequiresSourceAsset verifies the mandatory source gate.
func TestValidate_RequiresSourceAsset(t *testing.T) {
	req := &RenderRequest{}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Fatal("Validate must reject a missing source_asset_id")
	} else if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error must wrap ErrInvalidRequest, got %v", err)
	}
}

// TestValidate_BackgroundModes verifies the background mode enum gate.
func TestValidate_BackgroundModes(t *testing.T) {
	for _, mode := range []string{BackgroundModeNone, BackgroundModeBlurSource} {
		req := &RenderRequest{SourceAssetID: "a", Background: &BackgroundSpec{Mode: mode}}
		req.Normalize()
		if err := req.Validate(); err != nil {
			t.Errorf("mode %q must validate: %v", mode, err)
		}
	}
	// asset requires asset_id.
	req := &RenderRequest{SourceAssetID: "a", Background: &BackgroundSpec{Mode: BackgroundModeAsset}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("mode=asset without asset_id must fail")
	}
	req.Background.AssetID = "bg-1"
	if err := req.Validate(); err != nil {
		t.Errorf("mode=asset with asset_id must validate: %v", err)
	}
	// unknown mode.
	req = &RenderRequest{SourceAssetID: "a", Background: &BackgroundSpec{Mode: "mosaic"}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("unknown background mode must fail")
	}
}

// TestValidate_WatermarkContract verifies the watermark gate.
func TestValidate_WatermarkContract(t *testing.T) {
	// enabled without asset_id fails.
	req := &RenderRequest{SourceAssetID: "a", Watermark: &WatermarkSpec{Enabled: true}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("enabled watermark without asset_id must fail")
	}
	// invalid position fails.
	req = &RenderRequest{SourceAssetID: "a", Watermark: &WatermarkSpec{Enabled: true, AssetID: "wm", Position: "middle"}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("invalid watermark position must fail")
	}
	// opacity out of range fails.
	req = &RenderRequest{SourceAssetID: "a", Watermark: &WatermarkSpec{Enabled: true, AssetID: "wm", Opacity: 1.5}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("opacity > 1 must fail")
	}
	// negative margin fails.
	req = &RenderRequest{SourceAssetID: "a", Watermark: &WatermarkSpec{Enabled: true, AssetID: "wm", MarginPX: -4}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("negative margin must fail")
	}
	// valid watermark passes.
	req = &RenderRequest{SourceAssetID: "a", Watermark: &WatermarkSpec{Enabled: true, AssetID: "wm", Position: PositionTopRight, Opacity: 0.85, MarginPX: 40}}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Errorf("valid watermark must validate: %v", err)
	}
}

func TestValidate_TextWatermarkContract(t *testing.T) {
	req := &RenderRequest{SourceAssetID: "a", Watermark: &WatermarkSpec{
		Enabled: true, Text: "COMEDYTODAY", Position: PositionCenter, Opacity: 0.5,
	}}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Fatalf("text watermark must validate without asset_id: %v", err)
	}
}

// TestValidate_OutputContract verifies output resolution/fps bounds.
func TestValidate_OutputContract(t *testing.T) {
	// out-of-range dimensions fail.
	req := &RenderRequest{SourceAssetID: "a", Output: &OutputSpec{Width: 8, Height: 1080, FPSNum: 24, FPSDen: 1}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("width below MinDimension must fail")
	}
	// out-of-range fps fails.
	req = &RenderRequest{SourceAssetID: "a", Output: &OutputSpec{Width: 1920, Height: 1080, FPSNum: 1000, FPSDen: 1}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("fps above MaxFPS must fail")
	}
	// valid V1 output passes.
	req = &RenderRequest{SourceAssetID: "a", Output: &OutputSpec{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1}}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Errorf("valid output must validate: %v", err)
	}

	// V2 rejects non-canonical FPS at the request boundary.
	req = &RenderRequest{SourceAssetID: "a", Output: &OutputSpec{
		Contract: OutputContractVeloxAssemblyReadyV2, Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1,
	}}
	req.Normalize()
	if _, err := NewContractResolver().Resolve(nil, req); err == nil || !errors.Is(err, ErrOutputContractMismatch) {
		t.Fatalf("V2 30/1 must return OUTPUT_CONTRACT_MISMATCH, got %v", err)
	}

	// V2 accepts the exact canonical rational FPS.
	req.Output.FPSNum = 24
	req.Output.FPSDen = 1
	if _, err := NewContractResolver().Resolve(nil, req); err != nil {
		t.Fatalf("V2 24/1 must resolve: %v", err)
	}
	// Portrait and square outputs are intentionally removed from the
	// YouTube clip contract.
	for _, geometry := range [][2]int{{1080, 1920}, {1080, 1080}} {
		req = &RenderRequest{SourceAssetID: "a", Output: &OutputSpec{
			Width: geometry[0], Height: geometry[1], FPSNum: 24, FPSDen: 1,
		}}
		req.Normalize()
		if err := req.Validate(); err == nil {
			t.Errorf("vertical/square geometry %dx%d must be rejected", geometry[0], geometry[1])
		}
	}
}

// TestValidate_AudioAndTranscriptEnums verifies the remaining enum
// gates.
func TestValidate_AudioAndTranscriptEnums(t *testing.T) {
	for _, mode := range []string{AudioModeCopyIfCompatible, AudioModeTranscode} {
		req := &RenderRequest{SourceAssetID: "a", Audio: &AudioSpec{Mode: mode}}
		req.Normalize()
		if err := req.Validate(); err != nil {
			t.Errorf("audio mode %q must validate: %v", mode, err)
		}
	}
	req := &RenderRequest{SourceAssetID: "a", Audio: &AudioSpec{Mode: "bogus"}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("unknown audio mode must fail")
	}
	for _, mode := range []string{TranscriptModeReuse, TranscriptModeGenerate, TranscriptModeReuseOrGenerate} {
		req := &RenderRequest{SourceAssetID: "a", Transcript: &TranscriptSpec{Mode: mode}}
		req.Normalize()
		if err := req.Validate(); err != nil {
			t.Errorf("transcript mode %q must validate: %v", mode, err)
		}
	}
	req = &RenderRequest{SourceAssetID: "a", Transcript: &TranscriptSpec{Mode: "bogus"}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("unknown transcript mode must fail")
	}
}

// TestValidate_SubtitlesMode verifies the subtitles enum gate when
// enabled.
func TestValidate_SubtitlesMode(t *testing.T) {
	for _, mode := range []string{SubtitlesModeBurn, SubtitlesModeSidecar} {
		req := &RenderRequest{SourceAssetID: "a", Subtitles: &SubtitlesSpec{Enabled: true, Mode: mode}}
		req.Normalize()
		if err := req.Validate(); err != nil {
			t.Errorf("subtitles mode %q must validate: %v", mode, err)
		}
	}
	req := &RenderRequest{SourceAssetID: "a", Subtitles: &SubtitlesSpec{Enabled: true, Mode: "soft"}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("unknown subtitles mode must fail")
	}
}

// TestValidate_OverlayLineageAllOrNothing verifies the overlay lineage gate:
// a final video that declares an overlay must carry the complete chain
// (render job id + plan fingerprint + render key + source video asset id +
// the declared compositing window); a partial reference is rejected — a
// half-proven composition is never accepted.
func TestValidate_OverlayLineageAllOrNothing(t *testing.T) {
	full := OverlayRefSpec{
		RenderJobID:        "render-job-001",
		PlanFingerprint:    "fp-001",
		RenderKey:          "key-001",
		SourceVideoAssetID: "source-video-001",
		StartUS:            50000,
		EndUS:              950000,
	}

	req := &RenderRequest{SourceAssetID: "a", Overlay: &full}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Fatalf("complete overlay ref must validate: %v", err)
	}

	// Each missing field independently fails: the lineage is all-or-nothing.
	// start_us=0 is a legitimate window (overlay at t=0) so it is NOT in the
	// missing set — only the window sanity gate (end_us > start_us) applies.
	for name, missing := range map[string]func(*OverlayRefSpec){
		"render_job_id":         func(o *OverlayRefSpec) { o.RenderJobID = "" },
		"plan_fingerprint":      func(o *OverlayRefSpec) { o.PlanFingerprint = "" },
		"render_key":            func(o *OverlayRefSpec) { o.RenderKey = "" },
		"source_video_asset_id": func(o *OverlayRefSpec) { o.SourceVideoAssetID = "" },
		"end_us":                func(o *OverlayRefSpec) { o.EndUS = 0 },
	} {
		partial := full
		missing(&partial)
		req := &RenderRequest{SourceAssetID: "a", Overlay: &partial}
		req.Normalize()
		if err := req.Validate(); err == nil {
			t.Errorf("overlay ref missing %s must fail", name)
		} else if !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("overlay ref missing %s must wrap ErrInvalidRequest, got %v", name, err)
		}
	}

	// An invalid window (end <= start) is rejected: an untimed blend is
	// never composited.
	badWindow := full
	badWindow.StartUS = 950000
	badWindow.EndUS = 50000
	reqBad := &RenderRequest{SourceAssetID: "a", Overlay: &badWindow}
	reqBad.Normalize()
	if err := reqBad.Validate(); err == nil {
		t.Error("overlay ref with end_us <= start_us must fail")
	}

	// A negative start is rejected: compositing windows start at t>=0.
	negStart := full
	negStart.StartUS = -1
	reqNeg := &RenderRequest{SourceAssetID: "a", Overlay: &negStart}
	reqNeg.Normalize()
	if err := reqNeg.Validate(); err == nil {
		t.Error("overlay ref with negative start_us must fail")
	}

	// A nil overlay (plain subtitles/watermark clip) remains valid.
	plain := &RenderRequest{SourceAssetID: "a"}
	plain.Normalize()
	if err := plain.Validate(); err != nil {
		t.Errorf("plain clip without overlay must validate: %v", err)
	}
}
