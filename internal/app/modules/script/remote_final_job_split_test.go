package script

// remote_final_job_split_test.go pins the adapter's fail-closed gate for the
// split PREPARE. The runner half already refuses to start when the adapter
// reports the split disabled; these tests prove that the adapter itself never
// sends a base-only PREPARE to a Master that cannot FINALIZE it, and that every
// failure before the transport boundary is tagged as "not sent" so the runner
// can discard the durable intent instead of replaying it forever.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	segmentcontract "github.com/Marcuss-ops/PipelineGen/internal/kernel/media/finaljob"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	scriptfinaljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/script/finaljob"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/remotejob"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/renderinggen"
)

type splitTestSegments struct{}

type splitTestRendererCapabilities []string

func (c splitTestRendererCapabilities) Capabilities(context.Context) ([]string, error) {
	return append([]string(nil), c...), nil
}

func (splitTestSegments) PrepareFinalJobVideoSegment(context.Context, scriptfinaljob.RuntimeAssetV1, segmentcontract.VideoSegmentRequest) (scriptfinaljob.RuntimeAssetV1, error) {
	return scriptfinaljob.RuntimeAssetV1{}, nil
}

func splitTestAdapter(t *testing.T, capabilities []string) *RemoteFinalJobAdapter {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/jobs/capabilities" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"capabilities":[` + quoteCapabilities(capabilities) + `]}`))
	}))
	t.Cleanup(server.Close)
	adapter := &RemoteFinalJobAdapter{
		client:   remotejob.New(server.URL, "test-token"),
		segments: splitTestSegments{},
	}
	adapter.SetRendererCapabilitySource(splitTestRendererCapabilities(capabilities))
	return adapter
}

func quoteCapabilities(capabilities []string) string {
	quoted := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		quoted = append(quoted, `"`+capability+`"`)
	}
	return strings.Join(quoted, ",")
}

func TestImageEffectPreflightUsesLiveRendererCapabilities(t *testing.T) {
	request := scriptgen.GenerateRequest{OverlayStyle: &scriptpkg.OverlayStyleSpec{
		Image: &scriptpkg.OverlayImageStyleSpec{
			Shadow: &scriptpkg.OverlayShadowSpec{Enabled: true},
			Effects: []scriptpkg.ImageEffectSpec{
				{Type: "vignette"},
				{Type: "black_and_white"},
				{Type: "none"},
			},
		},
	}}
	want := []string{
		"overlay.effect.drop_shadow",
		"overlay.effect.saturation",
		"overlay.effect.vignette",
	}
	got := requiredImageEffectCapabilities(request)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("required effect capabilities = %v, want %v", got, want)
	}

	missing := splitTestAdapter(t, []string{"overlay.effect.vignette", "overlay.effect.saturation"})
	err := missing.CheckFinalJobCapabilities(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_GPU_EFFECT") || !strings.Contains(err.Error(), "overlay.effect.drop_shadow") {
		t.Fatalf("missing GPU effect error = %v, want named fail-closed capability error", err)
	}

	// The Master can advertise unrelated capabilities. GPU effects must still
	// be checked against the live RenderingGen queue inventory.
	masterReadyQueueMissing := splitTestAdapter(t, want)
	masterReadyQueueMissing.SetRendererCapabilitySource(splitTestRendererCapabilities{
		"overlay.effect.vignette", "overlay.effect.saturation",
	})
	err = masterReadyQueueMissing.CheckFinalJobCapabilities(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_GPU_EFFECT") || !strings.Contains(err.Error(), "overlay.effect.drop_shadow") {
		t.Fatalf("queue-missing effect error = %v, want live queue inventory to reject it", err)
	}

	ready := splitTestAdapter(t, want)
	if err := ready.CheckFinalJobCapabilities(context.Background(), request); err != nil {
		t.Fatalf("supported image effect stack failed preflight: %v", err)
	}
}

func TestImageEffectPreflightUsesRenderingGenQueueHTTPInventory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/capabilities" {
			t.Errorf("GPU preflight must query the RenderingGen queue, got %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`["overlay.effect.vignette"]`))
	}))
	defer server.Close()

	adapter := &RemoteFinalJobAdapter{client: remotejob.New(server.URL, "test-token")}
	adapter.SetRendererCapabilitySource(renderinggen.New(server.URL))
	request := scriptgen.GenerateRequest{OverlayStyle: &scriptpkg.OverlayStyleSpec{
		Image: &scriptpkg.OverlayImageStyleSpec{Effects: []scriptpkg.ImageEffectSpec{{Type: "drop_shadow"}}},
	}}
	err := adapter.CheckFinalJobCapabilities(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_GPU_EFFECT") || !strings.Contains(err.Error(), "overlay.effect.drop_shadow") {
		t.Fatalf("missing live queue capability error = %v, want named fail-closed error", err)
	}
	request.OverlayStyle.Image.Effects = []scriptpkg.ImageEffectSpec{{Type: "vignette"}}
	if err := adapter.CheckFinalJobCapabilities(context.Background(), request); err != nil {
		t.Fatalf("advertised live queue capability was rejected: %v", err)
	}
}

func TestImageNoneSentinelDoesNotRequireRendererCapability(t *testing.T) {
	request := scriptgen.GenerateRequest{OverlayStyle: &scriptpkg.OverlayStyleSpec{
		Image: &scriptpkg.OverlayImageStyleSpec{Effects: []scriptpkg.ImageEffectSpec{{Type: "none"}}},
	}}
	if got := requiredImageEffectCapabilities(request); len(got) != 0 {
		t.Fatalf("none sentinel requested capabilities %v", got)
	}
	adapter := splitTestAdapter(t, nil)
	if err := adapter.CheckFinalJobCapabilities(context.Background(), request); err != nil {
		t.Fatalf("none sentinel should not require GPU capability: %v", err)
	}
}

func TestFinalJobSplitGateHonorsTheOptInEnvironment(t *testing.T) {
	adapter := splitTestAdapter(t, []string{scriptgen.FinalJobOverlayProjectionCapability})

	t.Setenv("VELOX_FINAL_JOB_SPLIT_PREPARE", "off")
	if adapter.FinalJobSplitEnabled() {
		t.Fatal("VELOX_FINAL_JOB_SPLIT_PREPARE=off enabled the split")
	}
	_, err := adapter.PrepareFinalJobSource(context.Background(), "run-split-off", scriptgen.GenerateRequest{}, &scriptgen.GenerateResult{})
	if !errors.Is(err, scriptgen.ErrFinalJobSplitPrepareNotSent) {
		t.Fatalf("disabled split error = %v, want ErrFinalJobSplitPrepareNotSent", err)
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled split error = %v, want an explicit disabled reason", err)
	}

	for _, value := range []string{"on", "true", "1", "enabled"} {
		t.Setenv("VELOX_FINAL_JOB_SPLIT_PREPARE", value)
		if !adapter.FinalJobSplitEnabled() {
			t.Fatalf("VELOX_FINAL_JOB_SPLIT_PREPARE=%q did not enable the split", value)
		}
	}
}

func TestFinalJobSplitPrepareFailsClosedWithoutMasterCapability(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_SPLIT_PREPARE", "on")
	adapter := splitTestAdapter(t, nil)

	_, err := adapter.PrepareFinalJobSource(context.Background(), "run-split-no-capability", scriptgen.GenerateRequest{}, &scriptgen.GenerateResult{})
	if !errors.Is(err, scriptgen.ErrFinalJobSplitPrepareNotSent) {
		t.Fatalf("missing capability error = %v, want ErrFinalJobSplitPrepareNotSent", err)
	}
	if !strings.Contains(err.Error(), scriptgen.FinalJobOverlayProjectionCapability) {
		t.Fatalf("missing capability error = %v, want the missing capability name", err)
	}
}

func TestFinalJobSplitPrepareReportsAnUnbuildablePlanAsNotSent(t *testing.T) {
	t.Setenv("VELOX_FINAL_JOB_SPLIT_PREPARE", "on")
	adapter := splitTestAdapter(t, []string{scriptgen.FinalJobOverlayProjectionCapability})

	// The Master advertises the capability, so the failure happens while the
	// local base plan is built. A nil result has no canonical timeline: the
	// builder must fail before any PREPARE leaves the host.
	_, err := adapter.PrepareFinalJobSource(context.Background(), "run-split-no-plan", scriptgen.GenerateRequest{}, nil)
	if !errors.Is(err, scriptgen.ErrFinalJobSplitPrepareNotSent) {
		t.Fatalf("unbuildable plan error = %v, want ErrFinalJobSplitPrepareNotSent", err)
	}
	if !strings.Contains(err.Error(), "canonical timeline") {
		t.Fatalf("unbuildable plan error = %v, want the source preflight reason", err)
	}
}

func TestSplitPrepareNotSentTagsThePreSendFailure(t *testing.T) {
	if err := splitPrepareNotSent(nil); err != nil {
		t.Fatalf("nil error must stay nil, got %v", err)
	}
	cause := errors.New("capability inventory unreachable")
	err := splitPrepareNotSent(cause)
	if !errors.Is(err, scriptgen.ErrFinalJobSplitPrepareNotSent) {
		t.Fatalf("tagged error = %v, want ErrFinalJobSplitPrepareNotSent", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("tagged error = %v, want the cause preserved", err)
	}
}
