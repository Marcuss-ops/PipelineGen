package scriptgeneration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

func preflightGatePlan(t *testing.T) *capmodel.OverlayPlan {
	t.Helper()
	return &capmodel.OverlayPlan{
		SchemaVersion: capmodel.SchemaVersionPlan,
		PlanID:        "plan-preflight-gate",
		VideoID:       "video-preflight-gate",
		Language:      "en",
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
		FPSDen:        1,
		DurationMS:    2000,
		Items: []capmodel.OverlayItem{
			imagePreflightItem("img-preflight", "entity_image", "", ""),
		},
	}
}

// TestFinalJobPreRenderPreflightRejectsBeforeAnyRender is the regression the
// job audit asked for: an invalid promised image must fail BEFORE the first
// overlay render is submitted, not after the GPU stage has already been paid.
func TestFinalJobPreRenderPreflightRejectsBeforeAnyRender(t *testing.T) {
	plan := preflightGatePlan(t)
	result := &GenerateResult{
		OverlayPlan:    plan,
		SourceLanguage: "en",
		OverlayAudit:   &OverlayAudit{EntityImages: &OverlayAuditEntityImages{Required: false}},
	}
	enqueuer := &recordingOverlayRenderEnqueuer{}
	runner := &Runner{overlayRenderEnqueuer: enqueuer, log: zap.NewNop()}

	req := GenerateRequest{FinalJob: true, Render: scriptpkg.VideoRenderSpec{Enabled: true}, MaxImageOverlays: 4}
	req.MediaPlan.ProviderPolicy.InternetImages = media.MediaToggleEnabled

	_, err := runner.renderOverlayPlans(context.Background(), req, 0, result)
	if err == nil {
		t.Fatal("an unverifiable promised image must fail before the render")
	}
	if !strings.Contains(err.Error(), "final_job preflight") {
		t.Fatalf("error must name the preflight gate, got %v", err)
	}
	if enqueuer.captured() != nil {
		t.Fatal("no overlay render may be submitted before the image contract is verified")
	}
	if result.FinalJobPreflight != nil {
		t.Fatal("a failed preflight must not leave a pass marker")
	}
}

// TestFinalJobPreflightMarkerIsBoundToTheSealedPlan pins both halves of the
// marker contract: a matching digest skips the network verification, a changed
// plan re-arms it. A stale marker must never certify different pixels.
func TestFinalJobPreflightMarkerIsBoundToTheSealedPlan(t *testing.T) {
	plan := preflightGatePlan(t)
	result := &GenerateResult{
		OverlayPlan: plan,
		FinalJobPreflight: &FinalJobPreflight{
			PlanDigest: OverlayPlanRenderDigest(plan),
			Passed:     true,
		},
	}
	if err := finalJobImagePreflightOnce(context.Background(), true, result); err != nil {
		t.Fatalf("a marker for the current plan must skip re-verification: %v", err)
	}

	// Any plan fact covered by the digest (here the item timing) invalidates
	// the marker: the next verification must actually run and fail on the
	// unverifiable image.
	plan.Items[0].DurationUS += 1_000_000
	if finalJobPreflightCoversPlan(result) {
		t.Fatal("a changed plan must not be covered by the old marker")
	}
	if err := finalJobImagePreflightOnce(context.Background(), true, result); err == nil {
		t.Fatal("the changed plan must be re-verified and reject the unverifiable image")
	}
}

// TestFinalJobPreRenderPreflightMarkerReachesTheDurableResult pins the join:
// the render path runs on a snapshot, so a marker left only there would be
// lost and the late handoff would download every image again.
func TestFinalJobPreRenderPreflightMarkerReachesTheDurableResult(t *testing.T) {
	allowFinalJobImageLoopbackForTest(t)
	imageBytes, imageSHA := preflightPNGBytes(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes)
	}))
	defer server.Close()

	plan := preflightGatePlan(t)
	plan.Items[0].AssetRefs = []capmodel.OverlayAssetRef{{
		AssetID: "asset-preflight",
		SHA256:  imageSHA,
		URL:     server.URL + "/image.png",
	}}
	result := &GenerateResult{
		OverlayPlan:    plan,
		SourceLanguage: "en",
		OverlayAudit:   &OverlayAudit{EntityImages: &OverlayAuditEntityImages{Required: false}},
	}
	runner := &Runner{overlayRenderEnqueuer: &recordingOverlayRenderEnqueuer{}, log: zap.NewNop()}
	req := GenerateRequest{FinalJob: true, Render: scriptpkg.VideoRenderSpec{Enabled: true}, MaxImageOverlays: 4}
	req.MediaPlan.ProviderPolicy.InternetImages = media.MediaToggleEnabled

	outcomes, err := runner.renderOverlayPlans(context.Background(), req, 0, result)
	if err != nil {
		t.Fatalf("verified images must render: %v", err)
	}
	runner.applyOverlayRenderOutcomes("run-preflight", result, outcomes)
	if result.FinalJobPreflight == nil || !result.FinalJobPreflight.Passed {
		t.Fatal("the verified marker must reach the durable result through the render join")
	}
	if result.FinalJobPreflight.PlanDigest != OverlayPlanRenderDigest(result.OverlayPlan) {
		t.Fatal("the durable marker must name the sealed plan it verified")
	}
}

// TestFinalJobPreflightVerifiesImagesOnce pins the happy path: verification
// runs, the marker is stamped with the plan digest, and the second call (the
// late handoff) does not download the same image again.
func TestFinalJobPreflightVerifiesImagesOnce(t *testing.T) {
	allowFinalJobImageLoopbackForTest(t)
	imageBytes, imageSHA := preflightPNGBytes(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imageBytes)
	}))
	defer server.Close()

	plan := preflightGatePlan(t)
	plan.Items[0].AssetRefs = []capmodel.OverlayAssetRef{{
		AssetID: "asset-preflight",
		SHA256:  imageSHA,
		URL:     server.URL + "/image.png",
	}}
	result := &GenerateResult{OverlayPlan: plan}

	if err := finalJobImagePreflightOnce(context.Background(), true, result); err != nil {
		t.Fatalf("verified image must pass: %v", err)
	}
	if result.FinalJobPreflight == nil || !result.FinalJobPreflight.Passed {
		t.Fatal("a successful verification must stamp the durable marker")
	}
	if got, want := result.FinalJobPreflight.PlanDigest, OverlayPlanRenderDigest(plan); got != want {
		t.Fatalf("marker digest = %q, want the sealed plan digest %q", got, want)
	}
	if err := finalJobImagePreflightOnce(context.Background(), true, result); err != nil {
		t.Fatalf("the stamped plan must pass without re-verification: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("image fetched %d times, want 1: the marker must prevent a duplicate download", hits.Load())
	}
}
