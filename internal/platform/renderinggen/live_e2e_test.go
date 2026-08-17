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
	apple := mustRead(t, filepath.Join(fixtureRoot, "apple.png"))
	backgroundHash := sha256Hex(background)
	appleHash := sha256Hex(apple)
	if backgroundHash != capoverlay.GoldenBackgroundHash || appleHash != capoverlay.GoldenAppleHash {
		t.Fatalf("golden asset drift: background=%s apple=%s", backgroundHash, appleHash)
	}
	putObject(t, storeURL, backgroundHash, background)
	putObject(t, storeURL, appleHash, apple)

	jobID := getenvOr("PIPELINEGEN_E2E_JOB_ID", "pipelinegen-live-overlay-v1")
	plan := capoverlay.OverlayPlan{
		SchemaVersion:   capoverlay.SchemaVersionPlan,
		PlanID:          jobID,
		VideoID:         jobID,
		ProjectID:       "pipelinegen-live",
		Width:           1280,
		Height:          720,
		FPS:             30,
		RendererVersion: "chronon",
		Items: []capoverlay.OverlayItem{
			{ID: "background", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 5000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "background", URL: "assets/background.jpg", SHA256: backgroundHash}}},
			{ID: "image_overlay", TemplateID: "IMAGE_OVERLAY", StartMs: 1000, EndMs: 4000, AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "apple", URL: "assets/apple.png", SHA256: appleHash}}},
		},
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
	if ref.Artifact.SHA256 == "" || ref.Artifact.SizeBytes <= 0 || ref.Artifact.Width != 1280 || ref.Artifact.Height != 720 || ref.Artifact.DurationUS != 5_000_000 {
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
