package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

type stubImageProviderHealthProbe struct {
	err   error
	calls int
}

func (p *stubImageProviderHealthProbe) ProbeImageProviderHealth(context.Context) error {
	p.calls++
	return p.err
}

type stubVidRushProviderAvailabilityProbe struct {
	mu        sync.Mutex
	available map[string]bool
	providers []string
}

func (p *stubVidRushProviderAvailabilityProbe) ProbeVidRushProvider(_ context.Context, provider string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.providers = append(p.providers, provider)
	if p.available[provider] {
		return nil
	}
	return errors.New("provider not registered")
}

func (p *stubVidRushProviderAvailabilityProbe) callCount(provider string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, called := range p.providers {
		if called == provider {
			count++
		}
	}
	return count
}

func TestRunMediaPreflight_EnabledVidRushProviderRequiresRegistration(t *testing.T) {
	for _, provider := range []struct {
		name   string
		policy mediadomain.MediaProviderPolicy
	}{
		{name: "artlist", policy: mediadomain.MediaProviderPolicy{Artlist: mediadomain.MediaToggleEnabled}},
		{name: "youtube", policy: mediadomain.MediaProviderPolicy{YouTube: mediadomain.MediaToggleEnabled}},
		{name: "internet_images", policy: mediadomain.MediaProviderPolicy{InternetImages: mediadomain.MediaToggleEnabled}},
		{name: "image_generation", policy: mediadomain.MediaProviderPolicy{ImageGeneration: mediadomain.MediaToggleEnabled}},
	} {
		t.Run(provider.name+" unavailable fails closed", func(t *testing.T) {
			probe := &stubVidRushProviderAvailabilityProbe{available: map[string]bool{}}
			result := RunMediaPreflight(context.Background(), MediaPreflightInput{
				MediaPlan:                   mediadomain.MediaPlanSpec{ProviderPolicy: provider.policy},
				VidRushProviderAvailability: probe,
			})
			if !result.HasFailures() || !strings.Contains(result.Error(), "[vidrush_provider] "+provider.name) {
				t.Fatalf("preflight = %+v, want fail-closed %s provider error", result.Failures, provider.name)
			}
			if !isPermanentMediaPreflightFailure(result.AsError()) {
				t.Fatalf("missing %s provider must be classified as permanent", provider.name)
			}
			if probe.callCount(provider.name) != 1 {
				t.Fatalf("%s probe calls = %d, want 1", provider.name, probe.callCount(provider.name))
			}
		})
	}

	t.Run("image generation registration passes", func(t *testing.T) {
		probe := &stubVidRushProviderAvailabilityProbe{available: map[string]bool{"image_generation": true}}
		result := RunMediaPreflight(context.Background(), MediaPreflightInput{
			MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{
				ImageGeneration: mediadomain.MediaToggleEnabled,
			}},
			VidRushProviderAvailability: probe,
		})
		if result.HasFailures() {
			t.Fatalf("registered provider must pass preflight: %s", result.Error())
		}
	})

	t.Run("enabled provider without probe fails closed", func(t *testing.T) {
		result := RunMediaPreflight(context.Background(), MediaPreflightInput{
			MediaPlan: mediadomain.MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{
				ImageGeneration: mediadomain.MediaToggleEnabled,
			}},
		})
		if !result.HasFailures() || !strings.Contains(result.Error(), "provider availability probe not wired") {
			t.Fatalf("preflight = %+v, want missing provider probe failure", result.Failures)
		}
		if !isPermanentMediaPreflightFailure(result.AsError()) {
			t.Fatal("missing provider registry must be classified as a permanent preflight failure")
		}
	})

	t.Run("disabled provider is not required", func(t *testing.T) {
		probe := &stubVidRushProviderAvailabilityProbe{available: map[string]bool{}}
		result := RunMediaPreflight(context.Background(), MediaPreflightInput{VidRushProviderAvailability: probe})
		if result.HasFailures() {
			t.Fatalf("disabled provider must not fail preflight: %s", result.Error())
		}
		if probe.callCount("image_generation") != 0 {
			t.Fatalf("disabled image_generation was probed %d times", probe.callCount("image_generation"))
		}
	})
}

// TestRunnerRejectsUnavailableImageGenerationBeforeTextGeneration verifies the
// real media preflight prevents LLM work when an explicitly requested provider
// is missing from the composed VidRush registry.
func TestRunnerRejectsUnavailableImageGenerationBeforeTextGeneration(t *testing.T) {
	runner, repo, textGen, _, voiceoverGen, _, _ := newTestRunner()
	runner.SetMediaPreflight(providerAvailabilityPreflight{
		probe: &stubVidRushProviderAvailabilityProbe{available: map[string]bool{}},
	})

	req := defaultTestRequest()
	req.Docs = DocumentsConfig{}
	req.DocsEnabled = false
	req.Languages = nil
	req.Audio = "none"
	req.MediaPlan.ProviderPolicy.ImageGeneration = mediadomain.MediaToggleEnabled
	runID := "run-image-generation-provider-preflight"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}))

	runner.Execute(context.Background(), runID, req)

	final, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	require.NotNil(t, final)
	require.Equal(t, RunStatusFailed, final.Status)
	require.Equal(t, StagePreflight, final.FailedStage)
	require.Equal(t, "MEDIA_PREFLIGHT_FAILED", final.ErrorCode)
	require.Contains(t, final.ErrorMessage, "image_generation")
	require.Nil(t, final.NextRetryAt, "a frozen composition missing the enabled provider is terminal")
	require.Equal(t, 1, final.AttemptCount)
	require.Equal(t, 0, textGen.callCount, "LLM must not run before provider availability is verified")
	require.Equal(t, 0, voiceoverGen.callCount, "TTS must not run before provider availability is verified")
}

func TestRunnerRechecksUnavailableProviderOnResumeAfterPreflight(t *testing.T) {
	runner, repo, textGen, _, voiceoverGen, _, _ := newTestRunner()
	probe := &stubVidRushProviderAvailabilityProbe{available: map[string]bool{}}
	runner.SetMediaPreflight(providerAvailabilityPreflight{probe: probe})

	req := defaultTestRequest()
	req.Docs = DocumentsConfig{}
	req.DocsEnabled = false
	req.Languages = nil
	req.Audio = "none"
	req.MediaPlan.ProviderPolicy.ImageGeneration = mediadomain.MediaToggleEnabled
	runID := "run-image-generation-provider-resume-preflight"
	require.NoError(t, repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusFailed, CurrentStage: StageGeneratingSceneText,
		FailedStage: StageGeneratingSceneText, AttemptCount: 1,
	}))

	runner.Execute(context.Background(), runID, req)

	final, err := repo.Get(context.Background(), runID)
	require.NoError(t, err)
	require.NotNil(t, final)
	require.Equal(t, StagePreflight, final.FailedStage)
	require.Equal(t, "MEDIA_PREFLIGHT_FAILED", final.ErrorCode)
	require.Contains(t, final.ErrorMessage, "image_generation")
	require.Nil(t, final.NextRetryAt, "a frozen composition missing the enabled provider is terminal")
	require.Equal(t, 2, final.AttemptCount)
	require.Equal(t, 0, textGen.callCount, "resumed run must not repeat LLM work when provider is missing")
	require.Equal(t, 0, voiceoverGen.callCount, "resumed run must not repeat TTS work when provider is missing")
	require.Equal(t, 1, probe.callCount("image_generation"))
}

type providerAvailabilityPreflight struct {
	probe VidRushProviderAvailabilityProbe
}

func (p providerAvailabilityPreflight) Run(ctx context.Context, req GenerateRequest) PreflightResult {
	return RunMediaPreflight(ctx, MediaPreflightInput{
		MediaPlan:                   req.MediaPlan,
		VidRushProviderAvailability: p.probe,
	})
}

func (p providerAvailabilityPreflight) RunVidRushProviderAvailability(ctx context.Context, req GenerateRequest) PreflightResult {
	return RunVidRushProviderAvailabilityPreflight(ctx, req.MediaPlan, p.probe)
}

var _ MediaPreflight = providerAvailabilityPreflight{}

// TestRunMediaPreflight_ImageProviderHealthFailClosed certifies that a run
// whose image providers are all degraded fails the media preflight BEFORE any
// generation work, instead of discovering the failure after burning a full
// durable run.
func TestRunMediaPreflight_ImageProviderHealthFailClosed(t *testing.T) {
	probe := &stubImageProviderHealthProbe{err: errors.New("all retrieval providers down")}
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{ImageProviderHealth: probe})
	if !result.HasFailures() {
		t.Fatal("RunMediaPreflight = no failures, want an image_providers failure")
	}
	found := false
	for _, failure := range result.Failures {
		if failure.Category == "image_providers" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failures = %+v, want an image_providers entry", result.Failures)
	}
	if probe.calls != 1 {
		t.Fatalf("probe calls = %d, want 1", probe.calls)
	}
}

// TestRunMediaPreflight_ImageProviderHealthPasses certifies a healthy provider
// set is not a failure.
func TestRunMediaPreflight_ImageProviderHealthPasses(t *testing.T) {
	probe := &stubImageProviderHealthProbe{}
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{ImageProviderHealth: probe})
	if result.HasFailures() {
		t.Fatalf("RunMediaPreflight = %+v, want no failures", result.Failures)
	}
	if probe.calls != 1 {
		t.Fatalf("probe calls = %d, want 1", probe.calls)
	}
}

// TestRunMediaPreflight_NoImageProviderProbeIsNoop certifies a run that does
// not depend on image retrieval (nil probe) is unaffected.
func TestRunMediaPreflight_NoImageProviderProbeIsNoop(t *testing.T) {
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{})
	if result.HasFailures() {
		t.Fatalf("RunMediaPreflight = %+v, want no failures", result.Failures)
	}
}
