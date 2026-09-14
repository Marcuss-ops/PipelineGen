package cliprender

import (
	"errors"
	"strings"
	"testing"

	assetpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// backgroundSHA is a syntactically valid content address for plan fixtures.
const backgroundSHA = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func backgroundMat() *MaterializedAsset {
	return &MaterializedAsset{
		AssetID:   "asset-bg",
		LocalPath: "/scratch/asset-bg",
		SHA256:    backgroundSHA,
	}
}

// TestBackgroundKindFromMediaType pins the ONE mapping from the canonical
// asset taxonomy to the render vocabulary. It exists because the two catalogs
// in this repository spell the same content differently (kernel/asset uses
// image|clip|stock|image_video, the media DB also stores the transport spelling
// video), and a background plate whose family is guessed wrong is rendered by
// the wrong layer.
func TestBackgroundKindFromMediaType(t *testing.T) {
	cases := []struct {
		name      string
		mediaType string
		wantKind  string
		wantOK    bool
	}{
		{"kernel image", string(assetpkg.MediaTypeImage), BackgroundKindImage, true},
		{"mime png", "image/png", BackgroundKindImage, true},
		{"mime jpeg with params", "image/jpeg; charset=binary", BackgroundKindImage, true},
		{"mime webp uppercase", "IMAGE/WEBP", BackgroundKindImage, true},
		{"kernel clip", string(assetpkg.MediaTypeClip), BackgroundKindVideo, true},
		{"kernel stock", string(assetpkg.MediaTypeStock), BackgroundKindVideo, true},
		{"kernel image_video is a generated VIDEO", string(assetpkg.MediaTypeImageVideo), BackgroundKindVideo, true},
		{"transport spelling video", "video", BackgroundKindVideo, true},
		{"mime mp4", "video/mp4", BackgroundKindVideo, true},
		{"surrounding whitespace", "  image  ", BackgroundKindImage, true},
		{"audio is not a plate", string(assetpkg.MediaTypeAudio), "", false},
		{"document is not a plate", string(assetpkg.MediaTypeDocument), "", false},
		{"script is not a plate", string(assetpkg.MediaTypeScript), "", false},
		{"empty", "", "", false},
		{"unknown", "application/octet-stream", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, ok := BackgroundKindFromMediaType(tc.mediaType)
			if ok != tc.wantOK || kind != tc.wantKind {
				t.Fatalf("BackgroundKindFromMediaType(%q) = (%q, %v), want (%q, %v)", tc.mediaType, kind, ok, tc.wantKind, tc.wantOK)
			}
		})
	}
}

func TestIsBackgroundKind(t *testing.T) {
	for _, value := range []string{BackgroundKindImage, BackgroundKindVideo} {
		if !IsBackgroundKind(value) {
			t.Errorf("IsBackgroundKind(%q) = false, want true", value)
		}
	}
	// A near miss must not be normalised into a render decision.
	for _, value := range []string{"", "Image", "video ", "images", "mp4", "cover"} {
		if IsBackgroundKind(value) {
			t.Errorf("IsBackgroundKind(%q) = true, want false", value)
		}
	}
}

// TestBackgroundSpecKindValidation pins the request-level rules: an explicit
// kind is only meaningful for an asset background, and an unknown value is a
// typed error rather than a silent fallback.
func TestBackgroundSpecKindValidation(t *testing.T) {
	cases := []struct {
		name    string
		spec    *BackgroundSpec
		wantErr bool
	}{
		{name: "asset with image kind", spec: &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "a", Kind: BackgroundKindImage}},
		{name: "asset with video kind", spec: &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "a", Kind: BackgroundKindVideo}},
		{name: "asset without kind is derived later", spec: &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "a"}},
		{name: "unknown kind", spec: &BackgroundSpec{Mode: BackgroundModeAsset, AssetID: "a", Kind: "png"}, wantErr: true},
		{name: "kind without asset", spec: &BackgroundSpec{Mode: BackgroundModeAsset, Kind: BackgroundKindImage}, wantErr: true},
		{name: "none must not carry a kind", spec: &BackgroundSpec{Mode: BackgroundModeNone, Kind: BackgroundKindImage}, wantErr: true},
		{name: "blur_source must not carry a kind", spec: &BackgroundSpec{Mode: BackgroundModeBlurSource, Kind: BackgroundKindVideo}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseRenderRequest()
			req.Background = tc.spec
			req.Normalize()
			err := req.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected validation failure")
				}
				if !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("error %v is not ErrInvalidRequest", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

// TestCompile_BackgroundKindIsSealed proves the media family is part of the
// sealed plan for BOTH families and that the sealed hash covers it: the
// renderer must never have to infer image vs video from a filename.
func TestCompile_BackgroundKindIsSealed(t *testing.T) {
	for _, kind := range []string{BackgroundKindImage, BackgroundKindVideo} {
		t.Run(kind, func(t *testing.T) {
			in := baseCompileInput()
			in.Background = backgroundMat()
			in.BackgroundMode = BackgroundModeAsset
			in.BackgroundKind = kind

			plan, err := Compile(in)
			if err != nil {
				t.Fatalf("Compile failed: %v", err)
			}
			if plan.Background == nil || plan.Background.Kind != kind {
				t.Fatalf("background kind not sealed: %+v", plan.Background)
			}

			// The kind is a render decision, so it must be inside the digest:
			// mutating it after sealing has to break validation.
			tampered := plan
			if kind == BackgroundKindImage {
				tampered.Background = &PlanBackground{Mode: BackgroundModeAsset, Kind: BackgroundKindVideo, AssetID: plan.Background.AssetID, Path: plan.Background.Path, SHA256: plan.Background.SHA256}
			} else {
				tampered.Background = &PlanBackground{Mode: BackgroundModeAsset, Kind: BackgroundKindImage, AssetID: plan.Background.AssetID, Path: plan.Background.Path, SHA256: plan.Background.SHA256}
			}
			if err := tampered.Validate(); !errors.Is(err, ErrClipPlanDrift) {
				t.Fatalf("mutated kind must be rejected as drift, got %v", err)
			}
		})
	}
}

// TestCompile_BackgroundKindFailClosed pins that an unresolvable background
// family is refused before any process starts, instead of reaching a renderer
// that would have to guess.
func TestCompile_BackgroundKindFailClosed(t *testing.T) {
	in := baseCompileInput()
	in.Background = backgroundMat()
	in.BackgroundMode = BackgroundModeAsset
	// BackgroundKind deliberately unset.
	if _, err := Compile(in); !errors.Is(err, ErrInvalidClipPlan) {
		t.Fatalf("missing background kind must fail closed, got %v", err)
	}
	if _, err := Compile(in); err == nil || !strings.Contains(err.Error(), "background_kind") {
		t.Fatalf("error must name background_kind, got %v", err)
	}

	unknown := in
	unknown.BackgroundKind = "webp"
	if _, err := Compile(unknown); !errors.Is(err, ErrInvalidClipPlan) {
		t.Fatalf("unknown background kind must fail closed, got %v", err)
	}

	// The asset-less modes must not accept a kind either: it would be a
	// contradiction (the mode says there is no plate).
	blurred := baseCompileInput()
	blurred.BackgroundMode = BackgroundModeBlurSource
	blurred.BackgroundKind = BackgroundKindVideo
	if _, err := Compile(blurred); !errors.Is(err, ErrInvalidClipPlan) {
		t.Fatalf("blur_source with a kind must fail closed, got %v", err)
	}
}

// TestPlanValidate_BackgroundKindRequiredForAsset pins the post-seal gate: a
// hand-built plan (or a partial mutation) that declares an asset background
// without its family is rejected, not silently rendered.
func TestPlanValidate_BackgroundKindRequiredForAsset(t *testing.T) {
	plan := ClipRenderPlanV1{
		Version: PlanVersion,
		RunID:   "run-1",
		Source:  PlanSource{AssetID: "src", Path: "/scratch/src.mp4", SHA256: backgroundSHA},
		Background: &PlanBackground{
			Mode: BackgroundModeAsset, AssetID: "bg", Path: "/scratch/bg.png", SHA256: backgroundSHA,
		},
		Output: PlanOutput{
			ContractID: "c", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		},
		Audio:      PlanAudio{Mode: AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: "/scratch/out.mp4",
	}
	if err := plan.Seal(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("asset background without a kind must not validate")
	}

	plan.Background.Kind = BackgroundKindImage
	if err := plan.Seal(); err != nil {
		t.Fatalf("re-seal: %v", err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("image background must validate: %v", err)
	}
}
