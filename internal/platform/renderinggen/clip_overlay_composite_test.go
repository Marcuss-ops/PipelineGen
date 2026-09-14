package renderinggen

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// compositeFinalClipPlan builds the sealed plan of a FINAL clip that carries, in
// one plan, a materialized background, a text watermark and a REUSED entity
// overlay. It goes through cliprender.Compile — the production builder — so the
// plan under test is the exact object the render worker receives, not a
// hand-assembled approximation of it.
func compositeFinalClipPlan(t *testing.T) (cliprender.ClipRenderPlanV1, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		path := dir + "/" + name
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	sourceBody := "composite final clip source"
	bgBody := "composite final clip background"
	sourcePath := write("source.mp4", sourceBody)
	bgPath := write("background.mp4", bgBody)
	segmentPath := write("overlay-segment.mp4", "composite final clip overlay")

	plan, err := cliprender.Compile(cliprender.CompileInput{
		RunID:      "composite-clip-1",
		DurationMS: 8000,
		Source: &cliprender.MaterializedAsset{
			AssetID:   "source-asset-001",
			LocalPath: sourcePath,
			SHA256:    digest.SHA256Bytes([]byte(sourceBody)),
		},
		Background: &cliprender.MaterializedAsset{
			AssetID:   "bg-asset-001",
			LocalPath: bgPath,
			SHA256:    digest.SHA256Bytes([]byte(bgBody)),
		},
		BackgroundMode: cliprender.BackgroundModeAsset,
		BackgroundKind: cliprender.BackgroundKindVideo,
		WatermarkSpec: &cliprender.WatermarkSpec{
			Enabled:  true,
			Text:     "VeloxEditing",
			Position: cliprender.PositionTopRight,
			Opacity:  0.85,
			MarginPX: 48,
			Style:    &scriptpkg.VideoVisualStyleSpec{Font: "Montserrat", Color: "#FFFFFF", FontSizePX: 42},
		},
		ForegroundScalePercent: 80,
		Overlay: &cliprender.PlanOverlayInput{Segments: []cliprender.PlanOverlayInputSegment{{
			Segment: &cliprender.OverlaySegment{
				RenderJobID: "overlay-job-1",
				RenderKey:   "rk-overlay-1",
				LocalPath:   segmentPath,
				SHA256:      overlaySegmentSHA,
				SizeBytes:   2048,
			},
			StartMS: 1000,
			EndMS:   3000,
		}}},
		Contract: &cliprender.ResolvedContract{
			ContractID:   cliprender.OutputContractVeloxAssemblyReadyV1,
			Container:    "mp4",
			VideoCodec:   "h264",
			VideoProfile: "high",
			PixelFormat:  "yuv420p",
			Width:        1920,
			Height:       1080,
			FPSNum:       24,
			FPSDen:       1,
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		AudioMode:  cliprender.AudioModeCopyIfCompatible,
		OutputPath: dir + "/out.mp4",
	})
	if err != nil {
		t.Fatalf("compile composite final clip plan: %v", err)
	}
	return plan, segmentPath
}

// TestMapClipPlanToOverlayPlan_CompositeFinalClip pins the full-fidelity final
// clip: a plan declaring a background AND a text watermark AND a reused overlay
// must cross the queue as all three blocks at once — a video background, a
// watermarked text layer and one timed video_overlay item — so the clip is
// composited and encoded ONCE inside the Chronon render. Dropping any of the
// three here would ship a final clip missing a part the caller declared.
func TestMapClipPlanToOverlayPlan_CompositeFinalClip(t *testing.T) {
	plan, segmentPath := compositeFinalClipPlan(t)
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map composite plan: %v", err)
	}
	var doc struct {
		Background      *overlayBackground `json:"background,omitempty"`
		Watermark       *overlayWatermark  `json:"watermark,omitempty"`
		Items           []overlayItem      `json:"items"`
		ForegroundScale int                `json:"foreground_scale_percent,omitempty"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode composite overlay plan: %v", err)
	}

	if doc.Background == nil || doc.Background.Kind != "video" {
		t.Fatalf("background block = %+v, want a video background", doc.Background)
	}
	if len(doc.Background.AssetRefs) != 1 || doc.Background.AssetRefs[0].SHA256 != plan.Background.SHA256 {
		t.Fatalf("background asset_refs = %+v, want the certified background bytes", doc.Background.AssetRefs)
	}
	if want := hashAddressedPath(plan.Background.AssetID, "background.mp4"); doc.Background.AssetRefs[0].URL != want {
		t.Fatalf("background URL = %q, want %q", doc.Background.AssetRefs[0].URL, want)
	}

	if doc.Watermark == nil || doc.Watermark.Text != "VeloxEditing" {
		t.Fatalf("watermark block = %+v, want the declared text", doc.Watermark)
	}
	if doc.Watermark.FontRef == nil || doc.Watermark.FontRef.MediaType != "font/ttf" {
		t.Fatalf("text watermark must ship a font_ref, got %+v", doc.Watermark.FontRef)
	}
	if doc.Watermark.Position != cliprender.PositionTopRight {
		t.Fatalf("watermark position = %q, want %q", doc.Watermark.Position, cliprender.PositionTopRight)
	}

	if len(doc.Items) != 1 {
		t.Fatalf("items = %d, want exactly the one reused overlay item", len(doc.Items))
	}
	item := doc.Items[0]
	if item.Kind != SemanticKindVideoOverlay || item.TemplateID != SemanticTemplateVideoOverlay {
		t.Fatalf("item kind/template = %q/%q", item.Kind, item.TemplateID)
	}
	if item.StartMS != 1000 || item.EndMS != 3000 {
		t.Fatalf("overlay window = [%d, %d)ms, want [1000, 3000)", item.StartMS, item.EndMS)
	}
	if doc.ForegroundScale != 80 {
		t.Fatalf("foreground_scale_percent = %d, want 80", doc.ForegroundScale)
	}

	// The staged asset set must cover everything the plan references: the
	// source, the background, the watermark font and the overlay segment (with
	// its local path, so the prefetch uploads the exact bytes).
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		t.Fatalf("asset refs: %v", err)
	}
	byHash := map[string]assetRef{}
	for _, ref := range refs {
		byHash[ref.Hash] = ref
	}
	for _, want := range []struct{ name, hash string }{
		{"source", plan.Source.SHA256},
		{"background", plan.Background.SHA256},
		{"overlay segment", overlaySegmentSHA},
	} {
		if _, ok := byHash[want.hash]; !ok {
			t.Fatalf("%s (%s…) missing from the staged asset refs", want.name, want.hash[:12])
		}
	}
	if ref := byHash[overlaySegmentSHA]; ref.LocalPath != segmentPath {
		t.Fatalf("staged overlay LocalPath = %q, want %q", ref.LocalPath, segmentPath)
	}
	// The watermark font must be staged too: RenderingGen resolves burned text
	// glyphs from the job's asset list, so a missing font is a runtime failure.
	fontStaged := false
	for _, ref := range refs {
		if strings.HasSuffix(ref.LogicalPath, "Montserrat-Bold.ttf") {
			fontStaged = true
		}
	}
	if !fontStaged {
		t.Fatalf("the watermark font is not staged: %+v", refs)
	}
}
