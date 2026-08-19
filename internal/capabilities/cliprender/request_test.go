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
	if req.Output.Contract != OutputContractVeloxEditingClipV1 {
		t.Errorf("Output.Contract default: got %q, want velox-editing-clip-v1", req.Output.Contract)
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

// TestValidate_OutputContract verifies output resolution/fps bounds.
func TestValidate_OutputContract(t *testing.T) {
	// out-of-range dimensions fail.
	req := &RenderRequest{SourceAssetID: "a", Output: &OutputSpec{Width: 8, Height: 1920, FPSNum: 60, FPSDen: 1}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("width below MinDimension must fail")
	}
	// out-of-range fps fails.
	req = &RenderRequest{SourceAssetID: "a", Output: &OutputSpec{Width: 1080, Height: 1920, FPSNum: 1000, FPSDen: 1}}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Error("fps above MaxFPS must fail")
	}
	// valid passes.
	req = &RenderRequest{SourceAssetID: "a", Output: &OutputSpec{Width: 1080, Height: 1920, FPSNum: 60, FPSDen: 1}}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Errorf("valid output must validate: %v", err)
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
