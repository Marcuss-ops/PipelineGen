package renderinggen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"

	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

// Local aliases for the on-the-wire discriminator + lifecycle state values
// extracted from the central queue spec. The broker is the source of truth
// here so the certification is anchored to the wire, not to a refactored-side
// constant.
const (
	jobTypeOverlayRend = "overlay.render"
	stateCompleted     = "completed"
)

// TestOverlayRenderBrokerE2E certifies that an overlay.render job running
// the canonical timed plan (BACKGROUND 0-6000 ms, IMAGE 500-3500 ms, PHRASE
// 1000-4000 ms) lands on the broker in `state="completed"` with a fully
// populated artifact (sha256 + size_bytes>0 + drive_file_id/drive_link once
// published).
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	PIPELINEGEN_E2E_JOB_ID=overlay-render-broker-$(date +%s) \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	go test ./internal/platform/renderinggen/ -run TestOverlayRenderBrokerE2E -v
func TestOverlayRenderBrokerE2E(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live cross-repo overlay.render broker certification")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	// Pre-flight assets — the standard test fixtures.
	background := mustRead(t, filepath.Join(fixtureRoot, "background.mp4"))
	imageAsset := mustRead(t, filepath.Join(fixtureRoot, "overlay_globe.png"))
	backgroundHash := sha256Hex(background)
	imageHash := sha256Hex(imageAsset)
	if backgroundHash != capoverlay.GoldenBackgroundVideoHash || imageHash != capoverlay.GoldenGlobeHash {
		t.Fatalf("golden asset drift: bg=%s img=%s", backgroundHash, imageHash)
	}
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, imageHash, imageAsset)

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "overlay-render-broker")

	// Exactly the timeline the user pinned: BACKGROUND 0->6000, IMAGE 500->3500,
	// PHRASE 1000->4000. WORD is intentionally omitted — the test is
	// specifically about overlay.render with a 3-layer timed plan.
	plan := capoverlay.OverlayPlan{
		SchemaVersion:   capoverlay.SchemaVersionPlan,
		PlanID:          jobID,
		VideoID:         jobID,
		ProjectID:       "overlay-render-broker-cert",
		Width:           1280,
		Height:          720,
		FPS:             30,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{
				ID: "background_video", TemplateID: "VIDEO_BACKGROUND",
				StartMs: 0, EndMs: 6000,
				AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.mp4", SHA256: backgroundHash}},
			},
			{
				ID: "image", TemplateID: "IMAGE_OVERLAY", PresetID: "image_slide_left",
				StartMs: 500, EndMs: 3500,
				Params: map[string]any{"box_width": 260, "box_height": 260},
				AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "fixture", URL: "assets/overlay_globe.png", SHA256: imageHash}},
			},
			{
				ID: "phrase", TemplateID: "IMPORTANT_PHRASE", PresetID: "clean_slide_up",
				StartMs: 1000, EndMs: 4000,
				Text: "MICHAEL JORDAN CHANGED BASKETBALL",
			},
		},
	}

	compiled, err := capoverlay.CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile timed plan: %v", err)
	}

	// Pin the timeline so a regression in the timing projection never silently
	// shifts the rendered seconds away from the spec'd 0-6000/500-3500/1000-4000.
	wantTiming := map[string]struct{ StartMs, EndMs int64 }{
		"background_video": {0, 6000},
		"image":            {500, 3500},
		"phrase":           {1000, 4000},
	}
	for _, layer := range compiled.Plan.Layers {
		if want, ok := wantTiming[layer.ID]; ok {
			gotStart := int64(layer.StartFrame) * 1000 / int64(compiled.Plan.Canvas.FPS)
			gotDuration := int64(layer.DurationFrames) * 1000 / int64(compiled.Plan.Canvas.FPS)
			gotEnd := gotStart + gotDuration
			if gotStart != want.StartMs || gotEnd != want.EndMs {
				t.Fatalf("layer %s timing off: got [%d..%d] ms, want [%d..%d] ms",
					layer.ID, gotStart, gotEnd, want.StartMs, want.EndMs)
			}
		}
	}
	t.Logf("compiled plan timing pinned: BG 0-6000, IMAGE 500-3500, PHRASE 1000-4000 ✓")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatalf("enqueue overlay.render: %v", err)
	}
	if ref.JobID != jobID {
		t.Fatalf("ref.JobID = %q, want %q", ref.JobID, jobID)
	}
	if ref.Status != "COMPLETED" || ref.Artifact == nil {
		t.Fatalf("ref not COMPLETED: status=%s artifact=%+v", ref.Status, ref.Artifact)
	}
	if ref.Artifact.SHA256 == "" {
		t.Fatalf("ref.Artifact.SHA256 is empty")
	}
	if ref.Artifact.SizeBytes <= 0 {
		t.Fatalf("ref.Artifact.SizeBytes = %d (must be > 0)", ref.Artifact.SizeBytes)
	}
	if ref.Artifact.Width != 1280 || ref.Artifact.Height != 720 {
		t.Fatalf("ref.Artifact dimensions = %dx%d, want 1280x720", ref.Artifact.Width, ref.Artifact.Height)
	}
	if ref.Artifact.DurationUS != 6_000_000 {
		t.Fatalf("ref.Artifact.DurationUS = %d, want 6000000", ref.Artifact.DurationUS)
	}
	t.Logf("enqueuer returned COMPLETED: sha256=%s size=%d dims=%dx%d duration_us=%d",
		ref.Artifact.SHA256, ref.Artifact.SizeBytes, ref.Artifact.Width, ref.Artifact.Height, ref.Artifact.DurationUS)

	// 2. Hit the central queue directly with GET /jobs/{id} so the certification
	// is anchored to the broker's authoritative record, not to PipelineGen's
	// enqueuer-side wait loop.
	qc := queueclient.New(queueURL)
	job, err := qc.Get(ctx, jobID)
	if err != nil {
		t.Fatalf("GET /jobs/%s from broker: %v", jobID, err)
	}
	if job.JobType != jobTypeOverlayRend {
		t.Fatalf("broker.JobType = %q, want %q", job.JobType, jobTypeOverlayRend)
	}
	if job.State != queueclient.StateCompleted {
		t.Fatalf("broker.State = %q, want %q", job.State, queueclient.StateCompleted)
	}
	if job.Artifact == nil {
		t.Fatal("broker.Job.Artifact is nil despite state=completed")
	}
	if job.Artifact.ArtifactHash == "" {
		t.Fatal("broker.Artifact.ArtifactHash is empty")
	}
	if job.Artifact.SizeBytes <= 0 {
		t.Fatalf("broker.Artifact.SizeBytes = %d (must be > 0)", job.Artifact.SizeBytes)
	}
	if job.Artifact.ArtifactHash != ref.Artifact.SHA256 {
		t.Errorf("broker sha256 (%s) != enqueuer sha256 (%s)", job.Artifact.ArtifactHash, ref.Artifact.SHA256)
	}
	if job.Artifact.SizeBytes != ref.Artifact.SizeBytes {
		t.Errorf("broker size (%d) != enqueuer size (%d)", job.Artifact.SizeBytes, ref.Artifact.SizeBytes)
	}
	t.Logf("broker record certified: state=%s job_type=%s artifact_hash=%s size_bytes=%d storage_key=%s",
		job.State, job.JobType, job.Artifact.ArtifactHash, job.Artifact.SizeBytes, job.Artifact.StorageKey)

	// 3. Verify the artifact bytes are reachable from the object store under
	// the storage_key the broker reports.
	if job.Artifact.StorageKey != "" {
		resp, err := http.Get(storeURL + "/objects/" + job.Artifact.StorageKey)
		if err != nil {
			t.Errorf("objectstore GET %s: %v", job.Artifact.StorageKey, err)
		} else {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("objectstore GET returned status %d, want 200", resp.StatusCode)
			} else {
				body, _ := io.ReadAll(resp.Body)
				if len(body) != int(job.Artifact.SizeBytes) {
					t.Errorf("objectstore size %d != broker size %d", len(body), job.Artifact.SizeBytes)
				} else {
					t.Logf("objectstore reachable: storage_key=%s size=%d (matches broker record)",
						job.Artifact.StorageKey, len(body))
				}
			}
		}
	}

	// 4. Optional diagnostics: print the full broker record so a caller can
	// inspect drive_file_id / drive_link / metrics without re-running the test.
	rec, _ := json.MarshalIndent(filterBrokerJob(&job), "", "  ")
	t.Logf("full broker record:\n%s", string(rec))
}

// filterBrokerJob returns only the fields useful to a human auditor, hiding
// the noisy RenderPlan blob (~1KB of JSON) while keeping every observable
// promise in scope (state, job_type, attempts, timestamps, artifact).
func filterBrokerJob(j *queueclient.Job) map[string]any {
	return map[string]any{
		"id":              j.ID,
		"schema":          j.Schema,
		"version":         j.Version,
		"idempotency_key": j.IdempotencyKey,
		"job_type":        j.JobType,
		"state":           j.State,
		"worker":          j.Worker,
		"attempts":        j.Attempts,
		"created_at":      j.CreatedAt,
		"queued_at":       j.QueuedAt,
		"started_at":      j.StartedAt,
		"completed_at":    j.CompletedAt,
		"lease_until":     j.LeaseUntil,
		"fail_reason":     j.FailReason,
		"artifact": map[string]any{
			"id":                j.Artifact.ID,
			"storage_key":       j.Artifact.StorageKey,
			"artifact_url":      j.Artifact.ArtifactURL,
			"artifact_hash":     j.Artifact.ArtifactHash,
			"content_type":      j.Artifact.ContentType,
			"size_bytes":        j.Artifact.SizeBytes,
			"width":             j.Artifact.Width,
			"height":            j.Artifact.Height,
			"fps_num":           j.Artifact.FPSNum,
			"fps_den":           j.Artifact.FPSDen,
			"frame_count":       j.Artifact.FrameCount,
			"duration_us":       j.Artifact.DurationUS,
			"profile_id":        j.Artifact.ProfileID,
			"copy_eligible":     j.Artifact.CopyEligible,
			"codec":             j.Artifact.Codec,
			"backend":           j.Artifact.Backend,
			"chronon_version":   j.Artifact.ChrononVersion,
			"drive_file_id":     j.Artifact.DriveFileID,
			"drive_link":        j.Artifact.DriveLink,
			"container":         j.Artifact.Container,
			"pixel_format":      j.Artifact.PixelFormat,
			"audio_streams":     j.Artifact.AudioStreams,
		},
	}
}

var _ = fmt.Sprintf // keep fmt imported if the diagnostics above stop printing
var _ = strings.TrimSpace
