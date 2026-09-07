package renderinggen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"

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
}
