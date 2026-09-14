package renderinggen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/texttracks"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// mapperPlan builds a minimal sealed ClipRenderPlanV1 with subtitles declared
// in burn mode so the mapper must carry the typed style block (incl. stroke).
func mapperPlan(t *testing.T, style *scriptpkg.VideoVisualStyleSpec) cliprender.ClipRenderPlanV1 {
	t.Helper()
	sourcePath := t.TempDir() + "/source.mp4"
	sourceBytes := []byte("mapper source bytes")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceHash := fmt.Sprintf("%x", sha256.Sum256(sourceBytes))
	plan := cliprender.ClipRenderPlanV1{
		Version:    cliprender.PlanVersion,
		RunID:      "mapper-clip-1",
		DurationMS: 1000,
		Source:     cliprender.PlanSource{AssetID: "source-asset-001", Path: sourcePath, SHA256: sourceHash},
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeNone},
		Output:     cliprender.PlanOutput{ContractID: "VELOX_ASSEMBLY_READY_V1", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1},
		Audio:      cliprender.PlanAudio{Mode: cliprender.AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: t.TempDir() + "/out.mp4",
		Subtitles: &cliprender.PlanSubtitles{
			Mode:  cliprender.SubtitlesModeBurn,
			Style: style,
		},
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	return plan
}

// decodeOverlaySubtitlesStyle round-trips the mapper output back to the wire
// style block so tests can assert what actually travels to RenderingGen.
func decodeOverlaySubtitlesStyle(t *testing.T, raw []byte) *styleBlock {
	t.Helper()
	var doc struct {
		Subtitles *struct {
			Style *styleBlock `json:"style,omitempty"`
		} `json:"subtitles,omitempty"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode overlay plan: %v", err)
	}
	if doc.Subtitles == nil || doc.Subtitles.Style == nil {
		t.Fatal("mapper dropped the subtitles style block")
	}
	return doc.Subtitles.Style
}

// TestMapClipPlanToOverlayPlan_ExplicitStrokeWins verifies a caller-declared
// stroke block travels verbatim (width in render pixels) instead of the
// shadow-derived fallback.
func TestMapClipPlanToOverlayPlan_ExplicitStrokeWins(t *testing.T) {
	plan := mapperPlan(t, &scriptpkg.VideoVisualStyleSpec{
		Font: "Poppins", Color: "#FFFFFF", FontSizePX: 58, Position: "bottom_center",
		Shadow: &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 1, BlurPX: 2, OffsetX: 1, OffsetY: 1},
		Stroke: &scriptpkg.VideoStrokeSpec{Color: "#000000", Width: 3.5},
	})
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	s := decodeOverlaySubtitlesStyle(t, raw)
	if s.Stroke == nil {
		t.Fatal("explicit stroke was dropped on the wire")
	}
	if s.Stroke.Color != "#000000" || s.Stroke.Width != 3.5 {
		t.Fatalf("explicit stroke not verbatim: %+v", s.Stroke)
	}
	if s.Shadow == nil || s.Shadow.Color != "#000000" {
		t.Fatalf("shadow lost next to stroke: %+v", s.Shadow)
	}
}

// TestMapClipPlanToOverlayPlan_ShadowDerivedStrokeFallback locks the legacy
// behaviour: a style that declares only a shadow still yields the derived
// default contour so subtitle text never renders without an edge.
func TestMapClipPlanToOverlayPlan_ShadowDerivedStrokeFallback(t *testing.T) {
	plan := mapperPlan(t, &scriptpkg.VideoVisualStyleSpec{
		Font: "Poppins", Color: "#FFFFFF", FontSizePX: 58, Position: "bottom_center",
		Shadow: &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 1, BlurPX: 2, OffsetX: 1, OffsetY: 1},
	})
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	s := decodeOverlaySubtitlesStyle(t, raw)
	if s.Stroke == nil {
		t.Fatal("shadow-only style must still derive a stroke")
	}
	if s.Stroke.Color != "#000000" || s.Stroke.Width <= 0 {
		t.Fatalf("unexpected derived stroke: %+v", s.Stroke)
	}
	// The derived keyline scales with the em (~4.5%, capped at 3px). The
	// historical fixed 5px at 58px merged adjacent glyph contours into a
	// black smear on 40-rune caption lines.
	if s.Stroke.Width >= 5 {
		t.Fatalf("derived keyline is too heavy for a %gpx font: %+v", s.FontSizePX, s.Stroke)
	}
}

// TestMapClipPlanToOverlayPlan_SubtitleBoxFitsCaptionLines locks the burned
// caption box: RenderingGen centres the text inside the box declared here, so
// a one-line box pushes the two-line captions the ASS contract produces out of
// their safe area (and makes their keyline look inflated).
func TestMapClipPlanToOverlayPlan_SubtitleBoxFitsCaptionLines(t *testing.T) {
	plan := mapperPlan(t, &scriptpkg.VideoVisualStyleSpec{
		Font: "Montserrat", Color: "#FFFFFF", FontSizePX: 58, Position: "bottom_center",
		Shadow: &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 0.95, BlurPX: 6, OffsetX: 2, OffsetY: 3},
	})
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	s := decodeOverlaySubtitlesStyle(t, raw)
	lines := texttracks.DefaultShortFormPolicy().MaxLines
	want := int(math.Ceil(58*1.25)) * lines
	if s.HeightPX != want {
		t.Fatalf("subtitle box height = %d, want %d (font 58 x %d lines)", s.HeightPX, want, lines)
	}
}

// TestMapClipPlanToOverlayPlan_ExplicitSubtitleBoxWins verifies a caller that
// declares its own box keeps it verbatim.
func TestMapClipPlanToOverlayPlan_ExplicitSubtitleBoxWins(t *testing.T) {
	plan := mapperPlan(t, &scriptpkg.VideoVisualStyleSpec{
		Font: "Montserrat", Color: "#FFFFFF", FontSizePX: 58, Position: "bottom_center",
		HeightPX: 240, WidthPX: 1200,
		Shadow: &scriptpkg.VideoShadowSpec{Color: "#000000", Opacity: 0.95, BlurPX: 6, OffsetX: 2, OffsetY: 3},
	})
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	s := decodeOverlaySubtitlesStyle(t, raw)
	if s.HeightPX != 240 || s.WidthPX != 1200 {
		t.Fatalf("explicit subtitle box not verbatim: %+v", s)
	}
}

// backgroundPlan builds a sealed plan carrying an asset background of the
// given kind, with the plate materialized at localPath (the suffix is what the
// mapper negotiates with the declared family).
func backgroundPlan(t *testing.T, kind, localPath string) cliprender.ClipRenderPlanV1 {
	t.Helper()
	plan := mapperPlan(t, nil)
	plan.Subtitles = nil
	plan.Background = &cliprender.PlanBackground{
		Mode:    cliprender.BackgroundModeAsset,
		Kind:    kind,
		AssetID: "bg-asset-001",
		Path:    localPath,
		SHA256:  strings.Repeat("d", 64),
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	return plan
}

func decodeOverlayBackground(t *testing.T, raw []byte) *overlayBackground {
	t.Helper()
	var doc struct {
		Background *overlayBackground `json:"background,omitempty"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode overlay plan: %v", err)
	}
	if doc.Background == nil {
		t.Fatal("mapper emitted no background block")
	}
	return doc.Background
}

// TestMapClipPlanToOverlayPlan_ImageBackground is the Goal-3 contract: an
// image plate crosses the queue as an IMAGE background (kind=image) with a
// plate extension the worker's asset store can decode — not as the historical
// unconditional video layer named "background.mp4".
func TestMapClipPlanToOverlayPlan_ImageBackground(t *testing.T) {
	for _, suffix := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		t.Run(suffix, func(t *testing.T) {
			plan := backgroundPlan(t, cliprender.BackgroundKindImage, "/scratch/plate"+suffix)
			raw, err := MapClipPlanToOverlayPlan(plan)
			if err != nil {
				t.Fatalf("map image background: %v", err)
			}
			bg := decodeOverlayBackground(t, raw)
			if bg.Kind != "image" {
				t.Errorf("kind = %q, want image", bg.Kind)
			}
			if bg.Fit != "cover" {
				t.Errorf("fit = %q, want cover", bg.Fit)
			}
			if bg.Loop {
				t.Error("an image plate must not declare loop")
			}
			if len(bg.AssetRefs) != 1 {
				t.Fatalf("asset_refs = %d, want 1", len(bg.AssetRefs))
			}
			wantURL := "assets/semantic/bg-asset-001/background" + suffix
			if got := bg.AssetRefs[0].URL; got != wantURL {
				t.Errorf("url = %q, want %q", got, wantURL)
			}
			if got := bg.AssetRefs[0].MediaType; got != "image/png" {
				t.Errorf("media_type = %q, want image/png", got)
			}
		})
	}
}

// TestMapClipPlanToOverlayPlan_VideoBackground pins the video family: a video
// plate stays a looping, cover-cropped video layer, and the staged filename
// matches the wire URL (the queue uploads exactly that logical path).
func TestMapClipPlanToOverlayPlan_VideoBackground(t *testing.T) {
	plan := backgroundPlan(t, cliprender.BackgroundKindVideo, "/scratch/plate.mp4")
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map video background: %v", err)
	}
	bg := decodeOverlayBackground(t, raw)
	if bg.Kind != "video" || bg.Fit != "cover" || !bg.Loop {
		t.Fatalf("video background = %+v, want kind=video fit=cover loop=true", bg)
	}
	wantURL := "assets/semantic/bg-asset-001/background.mp4"
	if got := bg.AssetRefs[0].URL; got != wantURL {
		t.Errorf("url = %q, want %q", got, wantURL)
	}
	// The prefetch list and the plan must agree byte for byte, otherwise the
	// worker looks for an object the queue never uploaded.
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		t.Fatalf("overlayPlanAssets: %v", err)
	}
	found := false
	for _, ref := range refs {
		if ref.Hash == plan.Background.SHA256 {
			if ref.LogicalPath != wantURL {
				t.Fatalf("prefetch path %q != plan url %q", ref.LogicalPath, wantURL)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("background asset missing from the prefetch list")
	}
}

// TestMapClipPlanToOverlayPlan_BackgroundKindMismatchFailsClosed proves the
// mapper never stages bytes under a family they do not belong to: an image
// kind with a video suffix (or the reverse) is a mapping error, not a silently
// mis-declared layer.
func TestMapClipPlanToOverlayPlan_BackgroundKindMismatchFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		kind string
		path string
	}{
		{"image kind, video plate", cliprender.BackgroundKindImage, "/scratch/plate.mp4"},
		{"video kind, image plate", cliprender.BackgroundKindVideo, "/scratch/plate.png"},
		{"unknown kind", "gif", "/scratch/plate.gif"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := backgroundPlan(t, tc.kind, tc.path)
			if _, err := MapClipPlanToOverlayPlan(plan); err == nil {
				t.Fatal("expected a mapping error")
			}
			if _, err := overlayPlanAssets(plan); err == nil {
				t.Fatal("expected the prefetch list to reject the same mismatch")
			}
		})
	}
}

// TestMapClipPlanToOverlayPlan_BlurSourceEmitsHonouredFit pins the blur_source
// wire shape. The historical "blur_cover" was not a fit of the render contract
// and was silently downgraded to cover by the decoder; the mapper must emit a
// fit the renderer actually honours so the framing decision is explicit.
func TestMapClipPlanToOverlayPlan_BlurSourceEmitsHonouredFit(t *testing.T) {
	plan := mapperPlan(t, nil)
	plan.Subtitles = nil
	plan.Background = &cliprender.PlanBackground{Mode: cliprender.BackgroundModeBlurSource}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map blur_source background: %v", err)
	}
	if strings.Contains(string(raw), "blur_cover") {
		t.Fatalf("mapper emitted an unsupported fit: %s", raw)
	}
	bg := decodeOverlayBackground(t, raw)
	if bg.Kind != "video" || bg.Fit != "cover" || !bg.Loop {
		t.Fatalf("blur_source background = %+v, want kind=video fit=cover loop=true", bg)
	}
	if len(bg.AssetRefs) != 1 || bg.AssetRefs[0].SHA256 != plan.Source.SHA256 {
		t.Fatalf("blur_source must reuse the source asset: %+v", bg.AssetRefs)
	}
}
