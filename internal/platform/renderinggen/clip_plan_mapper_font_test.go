package renderinggen

import (
	"encoding/json"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestMapClipPlanToOverlayPlan_CarriesSubtitleFontRef pins the typography
// handoff: burn mode must send the resolved font ASSET (id + sha + url), not
// only its family name. Without it the worker rasterized the cues with the
// first .ttf it materialized, so a requested family was silently ignored.
func TestMapClipPlanToOverlayPlan_CarriesSubtitleFontRef(t *testing.T) {
	plan := mapperPlan(t, &scriptpkg.VideoVisualStyleSpec{
		Font: "Inter", Color: "#FFFFFF", FontSizePX: 58, Position: "bottom_center",
	})
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	var doc struct {
		Subtitles *struct {
			Mode    string           `json:"mode"`
			FontRef *overlayAssetRef `json:"font_ref"`
		} `json:"subtitles"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode overlay plan: %v", err)
	}
	if doc.Subtitles == nil || doc.Subtitles.FontRef == nil {
		t.Fatal("burn mode must carry the subtitle font_ref")
	}
	ref := doc.Subtitles.FontRef
	if ref.AssetID != FontInter || ref.SHA256 == "" || ref.URL == "" {
		t.Fatalf("font_ref = %+v, want the resolved Inter asset", ref)
	}

	// Sidecar subtitles never rasterize, so they must not force a font.
	sidecar := plan
	sidecar.Subtitles = &cliprender.PlanSubtitles{
		Mode:   cliprender.SubtitlesModeSidecar,
		Path:   plan.Subtitles.Path,
		SHA256: plan.Subtitles.SHA256,
	}
	sidecar.PlanSHA256 = ""
	if err := sidecar.Seal(); err != nil {
		t.Fatalf("seal sidecar plan: %v", err)
	}
	raw, err = MapClipPlanToOverlayPlan(sidecar)
	if err != nil {
		t.Fatalf("map sidecar plan: %v", err)
	}
	doc.Subtitles = nil
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode sidecar plan: %v", err)
	}
	if doc.Subtitles == nil || doc.Subtitles.FontRef != nil {
		t.Fatal("sidecar mode must not carry a burn font_ref")
	}
}
