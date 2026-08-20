package renderinggen

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestPrepareRenderCertification is a one-off diagnostic that mirrors the
// real PipelineGen flow: it submits BOTH the overlay.prepare job (with
// pre-timing intents) AND the overlay.render job (with the timing-frozen
// plan) for the SAME plan_id, then queries the broker for each job and
// verifies the certification contract:
//
//   - prepare-<plan_id>  job_type=overlay.prepare
//     schema_version=renderinggen.overlay-prepare.v1
//     state=completed
//     assets: SHA256 + URL when an image intent is included
//
//   - <plan_id>          job_type=overlay.render
//     artifact present, sha256 non-empty, size_bytes > 0
//
// Run with:
//
//	PIPELINEGEN_RENDERINGGEN_E2E=1 \
//	RENDERINGGEN_GOLDEN_DIR=<abs path to RenderingGen/testdata/golden> \
//	PIPELINEGEN_E2E_JOB_ID=prepare-render-cert-<unique> \
//	go test ./internal/platform/renderinggen/ -run TestPrepareRenderCertification -v
func TestPrepareRenderCertification(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR required")
	}

	// Materialize the entity image so the prepare phase can hash it
	background := mustRead(t, filepath.Join(fixtureRoot, "background.jpg"))
	globe := mustRead(t, filepath.Join(fixtureRoot, "overlay_globe.png"))
	backgroundHash := sha256Hex(background)
	globeHash := sha256Hex(globe)
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, globeHash, globe)

	planID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "prepare-render-cert")

	// === 1. Submit overlay.prepare with pre-timing intents ===
	prepareReq := capoverlay.PrepareRequest{
		SchemaVersion: capoverlay.SchemaVersionPrepare,
		PlanID:        planID,
		VideoID:       planID,
		ProjectID:     "prepare-render-cert",
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
						{AssetID: "mj_photo", URL: "assets/overlay_globe.png", SHA256: globeHash},
					},
				},
			},
		},
	}
	prepareClient := New(queueURL)
	prepEnq, err := scriptgen.NewQueuePrepareEnqueuer(prepareClient)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := prepEnq.EnqueuePrepare(ctx, prepareReq); err != nil {
		t.Fatalf("enqueue prepare: %v", err)
	}

	// === 2. Submit overlay.render with the timing-frozen plan ===
	plan := capoverlay.OverlayPlan{
		SchemaVersion:   capoverlay.SchemaVersionPlan,
		PlanID:          planID,
		VideoID:         planID,
		ProjectID:       "prepare-render-cert",
		Width:           1280,
		Height:          720,
		FPS:             30,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{ID: "background", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 4000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.jpg", SHA256: backgroundHash}}},
			{ID: "image_mj", TemplateID: "IMAGE_OVERLAY", StartMs: 500, EndMs: 3500, Params: map[string]any{"position": []any{640, 360}, "box_width": 400, "box_height": 400}, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "mj_photo", URL: "assets/overlay_globe.png", SHA256: globeHash}}},
		},
	}
	renderEnq, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	rref, err := renderEnq.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Logf("render enqueue: %v (render job still submitted for broker inspection)", err)
	} else {
		t.Logf("render ref: jobID=%s status=%s", rref.JobID, rref.Status)
	}

	// === 3. Poll the broker for both jobs ===
	prepareID := "prepare-" + planID
	t.Logf("polling broker: prepare=%s render=%s", prepareID, planID)

	prepState, prepPayload, _ := pollBroker(t, queueURL, prepareID, 90*time.Second)
	if prepState != "" {
		t.Logf("PREPARE state=%s", prepState)
	}
	if prepPayload != nil {
		// Validate the contract
		if prepPayload.JobType != capoverlay.JobTypePrepare {
			t.Errorf("prepare job_type=%q want %q", prepPayload.JobType, capoverlay.JobTypePrepare)
		}
		if prepPayload.SchemaVersion != capoverlay.SchemaVersionPrepare {
			t.Errorf("prepare schema_version=%q want %q", prepPayload.SchemaVersion, capoverlay.SchemaVersionPrepare)
		}
		if prepPayload.State != "completed" {
			t.Errorf("prepare state=%q want completed", prepPayload.State)
		}
		if len(prepPayload.Assets) == 0 {
			t.Errorf("prepare assets empty: expected SHA256+URL for the image intent")
		} else {
			for _, a := range prepPayload.Assets {
				if a.Hash == "" {
					t.Errorf("prepare asset missing hash: %+v", a)
				}
				// Broker calls it logical_path; render_plan.intents[].payload.asset_refs[].url
				// is the contract URL (already validated via the nested render_plan above).
				t.Logf("prepare asset: hash=%s logical_path=%s", a.Hash, a.URL)
			}
		}
		// The asset URL contract lives inside render_plan.intents[].payload.asset_refs[].url
		var spec renderPlanSpec
		if json.Unmarshal(prepPayload.OverlaySpec, &spec) == nil && len(spec.Intents) > 0 {
			if raw, ok := spec.Intents[0]["payload"].(map[string]any); ok {
				if refs, ok := raw["asset_refs"].([]any); ok && len(refs) > 0 {
					if ref0, ok := refs[0].(map[string]any); ok {
						url, _ := ref0["url"].(string)
						hash, _ := ref0["sha256"].(string)
						if url == "" || hash == "" {
							t.Errorf("prepare intent asset_ref missing url/sha256: %+v", ref0)
						} else {
							t.Logf("prepare intent asset_ref: url=%s sha256=%s", url, hash)
						}
					}
				}
			}
		}
	} else {
		t.Errorf("prepare job %q not found in broker", prepareID)
	}

	rendState, rendPayload, rendRaw := pollBroker(t, queueURL, planID, 90*time.Second)
	if rendState != "" {
		t.Logf("RENDER  state=%s", rendState)
	}
	if rendPayload != nil {
		if rendPayload.JobType != capoverlay.JobTypeRender {
			t.Errorf("render job_type=%q want %q", rendPayload.JobType, capoverlay.JobTypeRender)
		}
		if rendPayload.State != "completed" {
			t.Errorf("render state=%q want completed", rendPayload.State)
		}
		if rendPayload.Artifact == nil {
			t.Errorf("render artifact is nil")
		} else {
			art := rendPayload.Artifact
			// The broker stores the hash and URL under artifact_hash and
			// artifact_url (not the RenderArtifact tags sha256/url), so we
			// promote them via the raw JSON map for the contract check.
			artHash := art.SHA256
			artURL := art.URL
			if raw, ok := rendRaw[planID].(map[string]any); ok {
				if h, ok := raw["artifact_hash"].(string); ok && h != "" {
					artHash = h
				}
				if u, ok := raw["artifact_url"].(string); ok && u != "" {
					artURL = u
				}
			}
			if artHash == "" || art.SizeBytes <= 0 || art.Width != 1280 || art.Height != 720 {
				t.Errorf("render artifact incomplete: hash=%q size=%d %dx%d dur_us=%d url=%q",
					artHash, art.SizeBytes, art.Width, art.Height, art.DurationUS, artURL)
			} else {
				t.Logf("render artifact OK: id=%s sha256=%s size=%d %dx%d dur_us=%d url=%s",
					art.ID, artHash, art.SizeBytes, art.Width, art.Height, art.DurationUS, artURL)
			}
		}
	} else {
		t.Errorf("render job %q not found in broker", planID)
	}
}

// minimal broker representation
type brokerPayload struct {
	ID            string                       `json:"id"`
	Schema        string                       `json:"schema"`
	Version       int                          `json:"version"`
	JobType       string                       `json:"job_type"`
	SchemaVersion string                       `json:"schema_version"`
	State         string                       `json:"state"`
	OverlaySpec   json.RawMessage              `json:"render_plan"`
	Assets        []scriptgen.RenderQueueAsset `json:"assets"`
	Artifact      *scriptgen.RenderArtifact    `json:"artifact,omitempty"`
	Attempts      int                          `json:"attempts"`
	CreatedAt     string                       `json:"created_at"`
	StartedAt     string                       `json:"started_at"`
	CompletedAt   string                       `json:"completed_at"`
	Worker        string                       `json:"worker"`
}

// renderPlanSpec mirrors the nested structure the broker stores under
// "render_plan" for both overlay.prepare and overlay.render jobs. The
// prepare contract puts schema_version + plan_id + intents inside this
// envelope, while the render contract puts canvas + layers inside.
type renderPlanSpec struct {
	SchemaVersion string           `json:"schema_version"`
	PlanID        string           `json:"plan_id"`
	VideoID       string           `json:"video_id"`
	Intents       []map[string]any `json:"intents"`
	Layers        []map[string]any `json:"layers"`
}

func pollBroker(t *testing.T, queueURL, jobID string, timeout time.Duration) (string, *brokerPayload, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	rawArtifact := map[string]any{}
	for {
		payload := &brokerPayload{}
		req, _ := http.NewRequest(http.MethodGet, queueURL+"/jobs/"+jobID, nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == 200 {
			body, _ := readAll(resp.Body)
			resp.Body.Close()
			_ = json.Unmarshal(body, payload)
			var raw map[string]any
			if json.Unmarshal(body, &raw) == nil {
				if a, ok := raw["artifact"].(map[string]any); ok {
					rawArtifact[jobID] = a
				}
			}
			// Promote schema_version nested in render_plan.
			if len(payload.OverlaySpec) > 0 {
				var spec renderPlanSpec
				if json.Unmarshal(payload.OverlaySpec, &spec) == nil {
					if spec.SchemaVersion != "" {
						payload.SchemaVersion = spec.SchemaVersion
					}
				}
			}
			return payload.State, payload, rawArtifact
		}
		if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			return "", nil, nil
		}
		time.Sleep(2 * time.Second)
	}
}

func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}
