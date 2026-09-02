package renderinggen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// TestLivePipelineGenToRenderingGen is an opt-in cross-repository canary. It
// deliberately goes through PipelineGen's production queue adapter: semantic
// OverlayPlan -> CompileChrononPlan -> HTTP queue -> RenderingGen worker ->
// certified artifact. The local stack is intentionally not required for the
// normal unit-test suite; run with PIPELINEGEN_RENDERINGGEN_E2E=1.
func TestLivePipelineGenToRenderingGen(t *testing.T) {
	if os.Getenv("PIPELINEGEN_RENDERINGGEN_E2E") != "1" {
		t.Skip("set PIPELINEGEN_RENDERINGGEN_E2E=1 to run the live cross-repo canary")
	}
	queueURL := getenvOr("RENDERINGGEN_QUEUE_URL", "http://localhost:8081")
	storeURL := getenvOr("RENDERINGGEN_STORE_URL", "http://localhost:9000")
	fixtureRoot := os.Getenv("RENDERINGGEN_GOLDEN_DIR")
	if fixtureRoot == "" {
		t.Fatal("RENDERINGGEN_GOLDEN_DIR must point at RenderingGen/testdata/golden")
	}

	background := mustRead(t, filepath.Join(fixtureRoot, "background.jpg"))
	poppins := mustRead(t, filepath.Join(fixtureRoot, "Poppins-Bold.ttf"))
	backgroundHash := sha256Hex(background)
	poppinsHash := sha256Hex(poppins)
	if backgroundHash != capoverlay.GoldenBackgroundHash || poppinsHash != capoverlay.GoldenPresetFontHash {
		t.Fatalf("golden asset drift: background=%s poppins=%s", backgroundHash, poppinsHash)
	}
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, poppinsHash, poppins)

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "pipelinegen-live-overlay-v1")
	plan := capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        jobID,
		VideoID:       jobID,
		ProjectID:     "pipelinegen-live",
		Width:         640,
		Height:        360,
		FPSNum:        30, FPSDen: 1,
		RendererVersion: "chronon",
		Background:      &capoverlay.OverlayBackground{Kind: "color", Color: []float64{0.03, 0.03, 0.03, 1}},
		Items: []capoverlay.OverlayItem{
			// The box is 300×100, so [170,130] is the actual top-left
			// coordinate that centers it on the 640×360 canvas.
			{ID: "center_text", TemplateID: "IMPORTANT_WORD", PresetID: "static_text_smoke", Text: "CIAO", StartMs: 200, EndMs: 900, Params: map[string]any{"position": []any{170, 130}, "box_width": 300, "box_height": 100}},
			// Basic subtitle smoke: a timed, lower-screen text layer. It
			// exercises the same Chronon text path and timing contract used by
			// the subtitle compiler, while remaining compatible with the v2
			// render-plan schema (which has no subtitle_track layer).
			{ID: "subtitle_basic", TemplateID: "lower_third", PresetID: "static_text_smoke", Text: "Sottotitolo base", StartMs: 400, EndMs: 2000, Params: map[string]any{"position": []any{120, 295}, "box_width": 400, "box_height": 60}},
		},
	}
	compiled, err := capoverlay.CompileChrononPlan(plan)
	if err != nil {
		t.Fatalf("compile centered-text/subtitle smoke plan: %v", err)
	}
	if len(compiled.Plan.Layers) != 3 {
		t.Fatalf("expected background + centered text + subtitle layers, got %d", len(compiled.Plan.Layers))
	}
	if got := compiled.Plan.Layers[1].Position; len(got) != 2 || got[0] != 170 || got[1] != 130 {
		t.Fatalf("center text geometry is not centered: position=%v", got)
	}
	if got := compiled.Plan.Layers[2].Text; got != "Sottotitolo base" {
		t.Fatalf("subtitle text missing from Chronon plan: %q", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	enqueuer, err := scriptgen.NewQueueRenderEnqueuer(New(queueURL))
	if err != nil {
		t.Fatal(err)
	}
	ref, err := enqueuer.EnqueueChrononPlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if ref.JobID != jobID || ref.Status != "COMPLETED" || ref.Artifact == nil {
		t.Fatalf("unexpected live render reference: %+v", ref)
	}
	if ref.Artifact.SHA256 == "" || ref.Artifact.SizeBytes <= 0 || ref.Artifact.Width != 640 || ref.Artifact.Height != 360 || ref.Artifact.DurationUS < 1_900_000 || ref.Artifact.DurationUS > 2_100_000 {
		t.Fatalf("live artifact is not certified: %+v", ref.Artifact)
	}
	t.Logf("live PipelineGen -> RenderingGen PASS: job=%s artifact=%s sha256=%s size=%d duration_us=%d", jobID, ref.Artifact.ID, ref.Artifact.SHA256, ref.Artifact.SizeBytes, ref.Artifact.DurationUS)
}

func putObject(t *testing.T, baseURL, hash string, data []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, baseURL+"/objects/"+hash, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("object store PUT %s: HTTP %d: %s", hash, resp.StatusCode, body)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return b
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func getenvOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
