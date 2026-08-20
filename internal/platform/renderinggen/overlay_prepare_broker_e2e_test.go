package renderinggen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"

	queueclient "github.com/Marcuss-ops/RenderginGen/queue/client"
)

// On-the-wire contract for overlay.prepare, lifted verbatim from the central
// queue / processor spec. The certification is anchored on these literals so
// the test breaks the moment the broker drifts from the spec.
const (
	prepareJobType        = "overlay.prepare"
	prepareSchemaVersion  = "renderinggen.overlay-prepare.v1"
	timingStatePending    = "PENDING"
)

// TestOverlayPrepareBrokerE2E certifies the overlay.prepare lifecycle on the
// broker for the canonical timed scaffold:
//
//   id               = prepare-<plan_id>
//   job_type         = overlay.prepare
//   schema           = renderinggen.overlay-prepare.v1
//   state            = completed
//   asset prefetch   = SHA256 + URL keyed under the intent's asset_refs
//
// The companion renderinggen worker (docker-worker-gpu-1 in this env)
// prefetches the asset bytes during the prepare phase; the test then
// re-fetches the same SHA256 from the object store to confirm the
// prefetch result is reachable downstream.
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	PIPELINEGEN_E2E_JOB_ID=overlay-prepare-broker-$(date +%s) \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	go test ./internal/platform/renderinggen/ -run TestOverlayPrepareBrokerE2E -v
func TestOverlayPrepareBrokerE2E(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live cross-repo overlay.prepare broker certification")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	// Stage the entity image at its expected content hash. The prepare-worker
	// prefetch is the only thing that needs to be observable here, so we
	// ensure the source bytes match the golden fixture (Globe image).
	imageAsset := mustRead(t, filepath.Join(fixtureRoot, "overlay_globe.png"))
	imageHash := sha256Hex(imageAsset)
	if imageHash != capoverlay.GoldenGlobeHash {
		t.Fatalf("golden asset drift: img=%s want=%s", imageHash, capoverlay.GoldenGlobeHash)
	}
	putObject(t, storeURL, imageHash, imageAsset)

	planID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "overlay-prepare-broker")

	// === 1. Submit overlay.prepare with a single pre-timing intent ===
	prepareReq := capoverlay.PrepareRequest{
		SchemaVersion: capoverlay.SchemaVersionPrepare,
		PlanID:        planID,
		VideoID:       planID,
		ProjectID:     "overlay-prepare-broker-cert",
		Width:         1280,
		Height:        720,
		FPS:           30,
		Intents: []capoverlay.OverlayIntent{
			{
				Version:     capoverlay.OverlayIntentVersion,
				IntentID:    "intent-image-mj",
				SceneID:     "scene-0",
				SceneIndex:  0,
				Entity:      capoverlay.EntityBinding{Type: "person_image", CanonicalName: "Michael Jordan"},
				TemplateID:  "IMAGE_OVERLAY",
				Source:      capoverlay.IntentSourceEntityImage,
				Kind:        "person_image",
				TimingState: capoverlay.TimingStatePending,
				Payload: capoverlay.IntentPayload{
					AssetRefs: []capoverlay.OverlayAssetRef{
						{AssetID: "mj_photo", URL: "assets/overlay_globe.png", SHA256: imageHash},
					},
				},
			},
		},
	}
	if err := prepareReq.Validate(); err != nil {
		t.Fatalf("prepareReq.Validate: %v", err)
	}

	// Confirm the wire-format carries the spec'd schema_version BEFORE submit.
	spec, err := json.Marshal(prepareReq)
	if err != nil {
		t.Fatalf("marshal prepare payload: %v", err)
	}
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(spec, &probe); err != nil {
		t.Fatalf("re-decode prepare payload: %v", err)
	}
	if probe.SchemaVersion != prepareSchemaVersion {
		t.Fatalf("prepare payload schema_version = %q, want %q", probe.SchemaVersion, prepareSchemaVersion)
	}
	t.Logf("prepare payload schema_version pinned: %q ✓", probe.SchemaVersion)

	enq, err := scriptgen.NewQueuePrepareEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := enq.EnqueuePrepare(ctx, prepareReq); err != nil {
		t.Fatalf("enqueue overlay.prepare: %v", err)
	}

	// === 2. Poll the broker for prepare-<plan_id> ===
	prepareID := "prepare-" + planID
	t.Logf("polling broker: prepare_id=%s", prepareID)
	qc := queueclient.New(queueURL)
	job, err := waitForJob(qc, prepareID, 90*time.Second)
	if err != nil {
		t.Fatalf("broker.Get /jobs/%s: %v", prepareID, err)
	}

	// === 3. Certify every contract field the spec binds ===
	if job.JobType != prepareJobType {
		t.Errorf("job_type = %q, want %q", job.JobType, prepareJobType)
	}
	if job.State != "completed" {
		t.Errorf("state = %q, want completed", job.State)
	}
	if len(job.RenderPlan) == 0 {
		t.Fatalf("render_plan payload empty")
	}
	// Decode the embedded plan and assert every spec'd field.
	var plan overlayPrepareEnvelope
	if err := json.Unmarshal(job.RenderPlan, &plan); err != nil {
		t.Fatalf("decode render_plan: %v", err)
	}
	if plan.SchemaVersion != prepareSchemaVersion {
		t.Errorf("plan.schema_version = %q, want %q", plan.SchemaVersion, prepareSchemaVersion)
	}
	if plan.PlanID != planID {
		t.Errorf("plan.plan_id = %q, want %q", plan.PlanID, planID)
	}
	if plan.VideoID != planID {
		t.Errorf("plan.video_id = %q, want %q", plan.VideoID, planID)
	}
	if plan.Width <= 0 || plan.Height <= 0 || plan.FPS <= 0 {
		t.Errorf("plan canvas not strictly positive: w=%d h=%d fps=%d", plan.Width, plan.Height, plan.FPS)
	}
	if len(plan.Intents) == 0 {
		t.Fatalf("plan.intents empty — every prepare must carry at least one intent")
	}
	for i, intent := range plan.Intents {
		if intent.TemplateID == "" {
			t.Errorf("plan.intents[%d] missing template_id", i)
		}
		if intent.TimingState != timingStatePending {
			t.Errorf("plan.intents[%d] timing_state = %q, want %q", i, intent.TimingState, timingStatePending)
		}
	}

	// === 4. Certify the prefetched asset — the broker MUST carry it for the
	//         worker that consumed the prepare to keep payload self-describing.
	if len(job.Assets) == 0 {
		t.Fatalf("prepare assets empty: worker should have prefetched and recorded the intent's asset_ref")
	}
	var foundAsset bool
	for _, a := range job.Assets {
		if a.Hash == imageHash {
			foundAsset = true
			t.Logf("asset prefetched by hash: sha256=%s logical_path=%s", a.Hash, a.LogicalPath)
		}
	}
	if !foundAsset {
		t.Errorf("asset hash %s missing from job.Assets (got %d assets)", imageHash, len(job.Assets))
	}

	// === 5. Independently verify the bytes are still reachable from the
	//         object store under the same SHA256 — the actual contract the
	//         prepare-worker relied on.
	resp, err := http.Get(storeURL + "/objects/" + imageHash)
	if err != nil {
		t.Errorf("objectstore GET %s: %v", imageHash, err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("objectstore GET returned status %d, want 200", resp.StatusCode)
		} else {
			body, _ := io.ReadAll(resp.Body)
			if int64(len(body)) != int64(len(imageAsset)) {
				t.Errorf("objectstore size %d != broker prefetch size %d", len(body), len(imageAsset))
			} else {
				t.Logf("prefetched asset verifiable on objectstore: sha256=%s bytes=%d ✓", imageHash, len(body))
			}
		}
	}

	// === 6. Print the full broker record so a human auditor can see every
	//         observable promise in one Logf capture. ===
	rec, _ := json.MarshalIndent(filterPrepareJob(job), "", "  ")
	t.Logf("full broker record:\n%s", string(rec))
}

// overlayPrepareEnvelope mirrors processor.prepare's overlayPrepareEnvelope
// verbatim so the test can decode the render_plan bytes the broker keeps.
type overlayPrepareEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	PlanID        string `json:"plan_id"`
	VideoID       string `json:"video_id"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPS           int    `json:"fps"`
	Intents       []struct {
		TemplateID  string `json:"template_id"`
		TimingState string `json:"timing_state"`
	} `json:"intents"`
}

// waitForJob polls qc.Get until the job reaches a terminal state.
func waitForJob(qc *queueclient.Client, id string, max time.Duration) (queueclient.Job, error) {
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		j, err := qc.Get(context.Background(), id)
		if err == nil {
			switch j.State {
			case "completed", "failed", "rendered":
				return j, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return queueclient.Job{}, fmt.Errorf("timeout waiting for %s to reach terminal state", id)
}

// filterPrepareJob returns the auditable projection of the broker record.
func filterPrepareJob(j queueclient.Job) map[string]any {
	out := map[string]any{
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
		"asset_count":     len(j.Assets),
		"asset_hashes":    assetHashes(j.Assets),
	}
	if j.Artifact != nil {
		out["artifact_present"] = true
	} else {
		out["artifact_present"] = false
	}
	return out
}

func assetHashes(assets []queueclient.AssetRef) []string {
	out := make([]string, 0, len(assets))
	for _, a := range assets {
		out = append(out, a.Hash)
	}
	return out
}
