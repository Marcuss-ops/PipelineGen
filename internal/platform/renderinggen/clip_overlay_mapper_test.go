package renderinggen

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

// overlaySegmentSHA is a valid 64-hex digest for the materialized overlay
// segment referenced by the sealed plan.
var overlaySegmentSHA = strings.Repeat("e", 64)

// mapperOverlayPlan builds a sealed plan carrying a resolved entity overlay so
// the mapper must emit the single-pass video-overlay item.
func mapperOverlayPlan(t *testing.T) (cliprender.ClipRenderPlanV1, string) {
	t.Helper()
	sourcePath := t.TempDir() + "/source.mp4"
	sourceBytes := []byte("overlay mapper source")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	segmentPath := t.TempDir() + "/overlay-segment.mp4"
	if err := os.WriteFile(segmentPath, []byte("overlay segment bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan := cliprender.ClipRenderPlanV1{
		Version:    cliprender.PlanVersion,
		RunID:      "overlay-clip-1",
		DurationMS: 4000,
		Source:     cliprender.PlanSource{AssetID: "source-asset-001", Path: sourcePath, SHA256: fmt.Sprintf("%x", sha256.Sum256(sourceBytes))},
		Background: &cliprender.PlanBackground{Mode: cliprender.BackgroundModeNone},
		Output:     cliprender.PlanOutput{ContractID: "VELOX_ASSEMBLY_READY_V1", Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1},
		Audio:      cliprender.PlanAudio{Mode: cliprender.AudioModeCopyIfCompatible, Codec: "aac", SampleRate: 48000, Channels: 2},
		OutputPath: t.TempDir() + "/out.mp4",
		Overlay: &cliprender.PlanOverlay{
			RenderJobID: "render-overlay-001",
			RenderKey:   "rk-overlay-001",
			Path:        segmentPath,
			SHA256:      overlaySegmentSHA,
			SizeBytes:   2048,
			StartMS:     1000,
			EndMS:       3000,
		},
	}
	if err := plan.Seal(); err != nil {
		t.Fatal(err)
	}
	return plan, segmentPath
}

// TestMapClipPlanToOverlayPlan_EmitsSinglePassVideoOverlay pins the wire
// contract of the single-pass path: a sealed overlay becomes exactly one
// semantic item (kind video_overlay, template VIDEO_OVERLAY) whose window and
// asset_refs let RenderingGen composite it inside the same Chronon render.
func TestMapClipPlanToOverlayPlan_EmitsSinglePassVideoOverlay(t *testing.T) {
	plan, segmentPath := mapperOverlayPlan(t)
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	var doc struct {
		Items []overlayItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode overlay plan items: %v", err)
	}
	if len(doc.Items) != 1 {
		t.Fatalf("items = %d, want exactly 1 single-pass overlay item", len(doc.Items))
	}
	item := doc.Items[0]
	if item.Kind != SemanticKindVideoOverlay || item.TemplateID != SemanticTemplateVideoOverlay {
		t.Fatalf("item kind/template = %q/%q, want %q/%q", item.Kind, item.TemplateID, SemanticKindVideoOverlay, SemanticTemplateVideoOverlay)
	}
	if item.StartMS != 1000 || item.EndMS != 3000 {
		t.Fatalf("item window = [%d, %d)ms, want [1000, 3000)", item.StartMS, item.EndMS)
	}
	if len(item.Assets) != 1 {
		t.Fatalf("item asset_refs = %+v, want the overlay segment", item.Assets)
	}
	// overlay-plan.v1 declares preset_id/motion_id with minLength 1, so an
	// empty value must be OMITTED rather than sent as "". RenderingGen's
	// cross-repo contract test validates this exact shape against the schema.
	var rawItem map[string]json.RawMessage
	if err := json.Unmarshal(mustMarshalItem(t, item), &rawItem); err != nil {
		t.Fatalf("decode emitted item: %v", err)
	}
	for _, key := range []string{"preset_id", "motion_id"} {
		if _, present := rawItem[key]; present {
			t.Fatalf("item must omit %q when empty (minLength 1 in the published schema)", key)
		}
	}
	if item.Assets[0].SHA256 != overlaySegmentSHA || item.Assets[0].MediaType != "video/mp4" {
		t.Fatalf("item asset = %+v", item.Assets[0])
	}
	// The logical path the plan references must be the exact object the
	// prefetch stages (same helper, same arguments).
	wantPath := hashAddressedPath(item.Assets[0].AssetID, "overlay.mp4")
	if item.Assets[0].URL != wantPath {
		t.Fatalf("item asset URL = %q, want %q", item.Assets[0].URL, wantPath)
	}

	// The segment must be a staged asset ref carrying its LOCAL path, so the
	// object-store prefetch uploads the exact bytes the plan references.
	refs, err := overlayPlanAssets(plan)
	if err != nil {
		t.Fatalf("asset refs: %v", err)
	}
	var found *assetRef
	for i := range refs {
		if refs[i].Hash == overlaySegmentSHA {
			found = &refs[i]
		}
	}
	if found == nil {
		t.Fatalf("overlay segment missing from the staged asset refs: %+v", refs)
	}
	if found.LocalPath != segmentPath {
		t.Fatalf("staged overlay LocalPath = %q, want %q", found.LocalPath, segmentPath)
	}
	if found.LogicalPath != wantPath {
		t.Fatalf("staged overlay LogicalPath = %q, want %q", found.LogicalPath, wantPath)
	}
}

// mustMarshalItem re-marshals one emitted item so the test can assert on the
// exact JSON key set that crosses the queue boundary.
func mustMarshalItem(t *testing.T, item overlayItem) []byte {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("marshal item: %v", err)
	}
	return raw
}

// TestMapClipPlanToOverlayPlan_NoOverlayEmitsEmptyItems locks the default: a
// plain clip carries an explicit empty item array (never null), so the
// RenderingGen schema validator always sees a valid JSON array.
func TestMapClipPlanToOverlayPlan_NoOverlayEmitsEmptyItems(t *testing.T) {
	plan := mapperPlan(t, nil)
	plan.DurationMS = 1000
	raw, err := MapClipPlanToOverlayPlan(plan)
	if err != nil {
		t.Fatalf("map plan: %v", err)
	}
	var doc struct {
		Items []overlayItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode overlay plan items: %v", err)
	}
	if len(doc.Items) != 0 {
		t.Fatalf("items = %+v, want none without an overlay", doc.Items)
	}
}
