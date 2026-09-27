package renderinggen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestImageMotionCatalogRuntimeE2E renders every image motion through the
// local RenderingGen queue, sequentially over one fixed portrait. It is a
// visual canary only: it never submits a Velox Master/video-final job.
func TestImageMotionCatalogRuntimeE2E(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to render all image catalog motions")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}
	motions := imageCatalogMotionIDs(t)
	if len(motions) != 18 {
		t.Fatalf("catalog contains %d image motions, want 18", len(motions))
	}
	// Use the native 1920x1080 video fixture. The 1280x720 background.mp4
	// fixture makes Chronon encode a 1920x1080 file with a 1280x720 image
	// pinned to the top-left, which would invalidate the resolution canary.
	background := mustRead(t, filepath.Join(fixtureRoot, "Pale-Olive.mp4"))
	portrait := mustRead(t, filepath.Join(fixtureRoot, "gerard_butler.jpg"))
	backgroundHash, portraitHash := sha256Hex(background), sha256Hex(portrait)
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, portraitHash, portrait)

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "image-motion-catalog-"+time.Now().UTC().Format("20060102T150405Z"))
	const slotMS int64 = 3000
	durationMS := int64(len(motions)) * slotMS
	items := make([]capoverlay.OverlayItem, 0, len(motions)+1)
	items = append(items, capoverlay.OverlayItem{
		ID: "canary-label", TemplateID: "IMPORTANT_WORD", PresetID: "static_text_smoke",
		Text: "IMAGE MOTION CANARY · NO PHRASES", StartMs: 0, EndMs: durationMS,
		Params: map[string]any{"position": []any{180, 60}, "box_width": 1350, "box_height": 120},
	})
	for i, motion := range motions {
		start := int64(i) * slotMS
		items = append(items, capoverlay.OverlayItem{
			ID: "image-motion-" + motion, Kind: "image", TemplateID: "IMAGE_OVERLAY",
			PresetID: "image_slide_left", MotionID: motion,
			StartMs: start, EndMs: start + slotMS,
			Params:    map[string]any{"box_width": 518, "box_height": 518},
			AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: portraitHash, URL: "assets/canary/gerard_butler.jpg", SHA256: portraitHash, MediaType: "image/jpeg"}},
		})
	}
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan, PlanID: jobID, VideoID: jobID,
		ProjectID: "image-motion-catalog-canary", Width: 1920, Height: 1080,
		FPSNum: 24, FPSDen: 1, DurationMS: durationMS, RendererVersion: "chronon",
		Background: &capoverlay.OverlayBackground{Kind: "video", Fit: "cover", Loop: true,
			AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: backgroundHash, URL: "assets/Pale-Olive.mp4", SHA256: backgroundHash, MediaType: "video/mp4"}}},
		Items: items,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("validate image motion canary plan: %v", err)
	}
	for _, item := range plan.Items {
		if item.TemplateID == "IMPORTANT_PHRASE" {
			t.Fatal("image motion canary must contain zero phrase overlays")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatalf("render all 18 image motions: %v", err)
	}
	if ref.Artifact == nil || ref.Artifact.SHA256 == "" || ref.Artifact.SizeBytes <= 0 {
		t.Fatalf("image motion render did not return a certified artifact: status=%s artifact=%+v", ref.Status, ref.Artifact)
	}
	t.Logf("rendered all %d image motions locally: job=%s status=%s artifact=%s sha256=%s size=%d duration_us=%d; no Master final job submitted",
		len(motions), ref.JobID, ref.Status, ref.Artifact.URL, ref.Artifact.SHA256, ref.Artifact.SizeBytes, ref.Artifact.DurationUS)
}

func imageCatalogMotionIDs(t *testing.T) []string {
	t.Helper()
	var path string
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, "ChrononTemplate", "catalog", "chronontemplate_catalog.v1.json")
		if _, err := os.Stat(candidate); err == nil {
			path = candidate
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	if path == "" {
		t.Fatal("could not locate canonical ChrononTemplate motion catalog")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read canonical motion catalog %s: %v", path, err)
	}
	var catalog struct {
		Motions []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
		} `json:"motions"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatalf("decode canonical motion catalog: %v", err)
	}
	var ids []string
	for _, motion := range catalog.Motions {
		if motion.Category == "image_25d_clean_v1" || motion.Category == "overlay_v3_image" {
			ids = append(ids, motion.ID)
		}
	}
	sort.Strings(ids)
	return ids
}
