package cliprender

import (
	"context"
	"errors"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestRendererCapabilitiesSatisfies(t *testing.T) {
	full := RendererCapabilities{ChrononVulkan: true, ChrononNativeCertified: true}
	if !full.Satisfies(RendererCapabilities{ChrononVulkan: true, ChrononNativeCertified: true}) {
		t.Fatal("full capabilities must satisfy the chronon requirements")
	}
	if !full.Satisfies(RendererCapabilities{}) {
		t.Fatal("empty requirements must be satisfied by any capabilities")
	}

	partial := RendererCapabilities{ChrononVulkan: true}
	if partial.Satisfies(RendererCapabilities{ChrononNativeCertified: true}) {
		t.Fatal("missing certification must fail satisfaction")
	}
	if !partial.Satisfies(RendererCapabilities{ChrononVulkan: true}) {
		t.Fatal("present binary must satisfy a requirement for it")
	}
}

func TestNewRenderBackendRegistrySeedsCanonicalBackends(t *testing.T) {
	registry := NewRenderBackendRegistry()
	backends := registry.Backends()
	if len(backends) != 2 {
		t.Fatalf("backends = %v, want [chronon_vulkan ffmpeg_fallback]", backends)
	}
	if backends[0] != BackendChrononVulkan || backends[1] != BackendFFmpegFallback {
		t.Fatalf("backends order = %v, want chronon_vulkan preferred (primary), then ffmpeg_fallback (the PATH B CUDA hybrid was removed)", backends)
	}
	// ffmpeg fallback runs on an empty host.
	if !registry.CanRun(BackendFFmpegFallback, RendererCapabilities{}) {
		t.Fatal("ffmpeg fallback must run on empty capabilities")
	}
	// chronon requires the certification gate.
	if registry.CanRun(BackendChrononVulkan, RendererCapabilities{}) {
		t.Fatal("chronon must not run without certification")
	}
	// Unknown backend never runs.
	if registry.CanRun(RenderBackend("bogus"), RendererCapabilities{}) {
		t.Fatal("unknown backend must not be runnable")
	}
}

func TestRenderBackendResolverPrefersChrononWhenCertified(t *testing.T) {
	resolver := NewRenderBackendResolver(nil)

	// Empty host (CPU-only worker) → software fallback.
	backend, err := resolver.Resolve(context.Background(), ClipRenderPlanV1{}, RendererCapabilities{})
	if err != nil {
		t.Fatalf("Resolve(empty caps): %v", err)
	}
	if backend != BackendFFmpegFallback {
		t.Fatalf("backend = %q, want ffmpeg_fallback", backend)
	}

	// GPU facts are irrelevant for resolution since the PATH B CUDA hybrid
	// was removed: a host WITHOUT Chronon certification resolves to the
	// software fallback, never to a GPU compositing path.
	uncertified := RendererCapabilities{ChrononVulkan: true}
	backend, err = resolver.Resolve(context.Background(), ClipRenderPlanV1{}, uncertified)
	if err != nil {
		t.Fatalf("Resolve(uncertified chronon): %v", err)
	}
	if backend != BackendFFmpegFallback {
		t.Fatalf("backend = %q, want ffmpeg_fallback (no certified chronon)", backend)
	}

	// Certified Chronon → chronon_vulkan (PRIMARY — preferred over software
	// for every plan, certified hosts render through Chronon exclusively).
	certified := RendererCapabilities{ChrononVulkan: true, ChrononNativeCertified: true}
	backend, err = resolver.Resolve(context.Background(), ClipRenderPlanV1{}, certified)
	if err != nil {
		t.Fatalf("Resolve(certified): %v", err)
	}
	if backend != BackendChrononVulkan {
		t.Fatalf("backend = %q, want chronon_vulkan", backend)
	}
}

func TestRenderBackendResolverFailClosedWhenNoBackendRuns(t *testing.T) {
	registry := NewRenderBackendRegistry()
	// Remove the software fallback so only the Chronon backend remains — a
	// host the certification gate refuses then has no runnable backend.
	registry.capabilities = map[RenderBackend]RendererCapabilities{
		BackendChrononVulkan: {ChrononVulkan: true, ChrononNativeCertified: true},
	}
	registry.order = []RenderBackend{BackendChrononVulkan}
	resolver := NewRenderBackendResolver(registry)

	_, err := resolver.Resolve(context.Background(), ClipRenderPlanV1{}, RendererCapabilities{})
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable, got %v", err)
	}
}

// requirementsPlan builds a plan with the given visual layers so the
// requirement-derivation tests exercise real field shapes.
func requirementsPlan(backgroundMode string, wm *PlanWatermark, subs *PlanSubtitles) ClipRenderPlanV1 {
	plan := ClipRenderPlanV1{}
	if backgroundMode != "" {
		plan.Background = &PlanBackground{Mode: backgroundMode}
	}
	if wm != nil {
		plan.Watermark = wm
	}
	if subs != nil {
		plan.Subtitles = subs
	}
	return plan
}

func TestRenderRequirementResolver_DerivesFromPlan(t *testing.T) {
	// Blur background + image watermark + burn subtitles with a shadow.
	plan := requirementsPlan(
		BackgroundModeBlurSource,
		&PlanWatermark{Text: "", Position: PositionTopRight, Opacity: 1},
		&PlanSubtitles{Mode: SubtitlesModeBurn, Path: "/x.ass", SHA256: strings.Repeat("a", 64)},
	)
	plan.Watermark.Style = &scriptpkg.VideoVisualStyleSpec{Shadow: &scriptpkg.VideoShadowSpec{Opacity: 0.5}}
	reqs := (RenderRequirementResolver{}).Resolve(plan)
	if !reqs.NeedsBackground || !reqs.NeedsBlurBackground {
		t.Errorf("background reqs not derived: %+v", reqs)
	}
	if !reqs.NeedsImageWatermark || reqs.NeedsTextWatermark {
		t.Errorf("watermark reqs not derived: %+v", reqs)
	}
	if !reqs.NeedsShadow || !reqs.NeedsBurnSubtitles || !reqs.NeedsGPUAlpha {
		t.Errorf("shadow/subtitles/alpha reqs not derived: %+v", reqs)
	}

	// Text watermark + subtitle transition → text requirement, transition.
	plan2 := requirementsPlan(
		"",
		&PlanWatermark{Text: "LOGO", Position: PositionCenter, Opacity: 0.9},
		&PlanSubtitles{Mode: SubtitlesModeSidecar, Path: "/x.ass", SHA256: strings.Repeat("b", 64)},
	)
	plan2.Subtitles.Style = &scriptpkg.VideoVisualStyleSpec{TransitionIn: &scriptpkg.VideoTransitionSpec{Preset: "fade_in", DurationMS: 120}}
	reqs2 := (RenderRequirementResolver{}).Resolve(plan2)
	if !reqs2.NeedsTextWatermark || reqs2.NeedsImageWatermark {
		t.Errorf("text watermark reqs not derived: %+v", reqs2)
	}
	if !reqs2.NeedsTransition || reqs2.NeedsShadow {
		t.Errorf("transition reqs not derived: %+v", reqs2)
	}
	if reqs2.NeedsBurnSubtitles {
		t.Errorf("sidecar subtitles must not need burn: %+v", reqs2)
	}

	// Empty plan → no requirements at all.
	if empty := (RenderRequirementResolver{}).Resolve(ClipRenderPlanV1{}); empty != (RenderRequirements{}) {
		t.Errorf("empty plan must derive zero requirements, got %+v", empty)
	}

	// Foreground scale != 100 → NeedsScale (fit/pad has no device-local CUDA
	// equivalent; 0 is the unset sentinel normalized to 100 by Compile).
	scaled := ClipRenderPlanV1{Output: PlanOutput{ForegroundScalePercent: 50}}
	if !(RenderRequirementResolver{}).Resolve(scaled).NeedsScale {
		t.Errorf("scale=50 plan must derive NeedsScale")
	}
	if (RenderRequirementResolver{}).Resolve(ClipRenderPlanV1{Output: PlanOutput{ForegroundScalePercent: 100}}).NeedsScale {
		t.Errorf("scale=100 plan must not derive NeedsScale")
	}
}

func TestRenderBackendResolver_RoutesByPlanRequirements(t *testing.T) {
	resolver := NewRenderBackendResolver(nil)
	wmPlan := requirementsPlan("", &PlanWatermark{Position: PositionTopRight, Opacity: 1}, nil)
	blurPlan := requirementsPlan(BackgroundModeBlurSource, nil, nil)
	scaledPlan := ClipRenderPlanV1{Output: PlanOutput{ForegroundScalePercent: 50}}
	allPlans := map[string]ClipRenderPlanV1{
		"wm plan":     wmPlan,
		"blur plan":   blurPlan,
		"plain":       {},
		"scaled plan": scaledPlan,
	}

	// Without certified Chronon every plan routes to the software baseline:
	// the PATH B CUDA hybrid that used to absorb the device-local class was
	// removed, so there is no GPU path outside Chronon.
	for name, plan := range allPlans {
		backend, err := resolver.Resolve(context.Background(), plan, RendererCapabilities{ChrononVulkan: true})
		if err != nil {
			t.Fatalf("Resolve(%s, no chronon): %v", name, err)
		}
		if backend != BackendFFmpegFallback {
			t.Fatalf("%s backend = %q, want ffmpeg_fallback", name, backend)
		}
	}

	// The same plans with Chronon CERTIFIED → chronon_vulkan. Chronon is the
	// PRIMARY backend: it satisfies the full requirement set (blur, scale,
	// shadows, transitions), so a certified host renders every plan through
	// Chronon.
	certified := RendererCapabilities{ChrononVulkan: true, ChrononNativeCertified: true}
	for name, plan := range allPlans {
		backend, err := resolver.Resolve(context.Background(), plan, certified)
		if err != nil {
			t.Fatalf("Resolve(%s, chronon certified): %v", name, err)
		}
		if backend != BackendChrononVulkan {
			t.Fatalf("%s backend = %q, want chronon_vulkan", name, backend)
		}
	}
}

// TestRenderBackendResolver_GatesChrononOnCertificationNotBinaryPresence
// locks the review's core rule: a configured Chronon binary (ChrononVulkan)
// WITHOUT a real certification (ChrononNativeCertified) is NEVER selected —
// a broken handoff must not add latency to every render. Only the certified
// flag opens the chronon_vulkan path.
func TestRenderBackendResolver_GatesChrononOnCertificationNotBinaryPresence(t *testing.T) {
	resolver := NewRenderBackendResolver(nil)
	blurPlan := requirementsPlan(BackgroundModeBlurSource, nil, nil)

	// Binary configured but NOT certified → the resolver must skip Chronon
	// and land on FFmpeg.
	uncertified := RendererCapabilities{ChrononVulkan: true}
	backend, err := resolver.Resolve(context.Background(), blurPlan, uncertified)
	if err != nil {
		t.Fatalf("Resolve(blur, chronon uncertified): %v", err)
	}
	if backend != BackendFFmpegFallback {
		t.Fatalf("uncertified chronon backend = %q, want ffmpeg_fallback (certification gate)", backend)
	}

	// Same host, certified → chronon_vulkan wins.
	uncertified.ChrononNativeCertified = true
	backend, err = resolver.Resolve(context.Background(), blurPlan, uncertified)
	if err != nil {
		t.Fatalf("Resolve(blur, chronon certified): %v", err)
	}
	if backend != BackendChrononVulkan {
		t.Fatalf("certified chronon backend = %q, want chronon_vulkan", backend)
	}

	// A certified flag WITHOUT binary presence must also fail closed (the
	// certifier only sets the flag after a successful binary run, so this is
	// defensive — never trust the flag alone).
	orphanCert := RendererCapabilities{ChrononNativeCertified: true}
	backend, err = resolver.Resolve(context.Background(), blurPlan, orphanCert)
	if err != nil {
		t.Fatalf("Resolve(blur, orphan cert): %v", err)
	}
	if backend != BackendFFmpegFallback {
		t.Fatalf("orphan cert backend = %q, want ffmpeg_fallback", backend)
	}
}

func TestRenderBackendResolver_FailsClosedWhenNoBackendSupportsRequirements(t *testing.T) {
	// Only the software fallback registered with NO support declaration — it
	// can satisfy nothing (fail-closed), so a plan with requirements resolves
	// to ErrBackendUnavailable instead of a silent downgrade.
	registry := NewRenderBackendRegistry()
	registry.capabilities = map[RenderBackend]RendererCapabilities{BackendFFmpegFallback: {}}
	registry.supports = map[RenderBackend]BackendSupport{BackendFFmpegFallback: {}}
	registry.order = []RenderBackend{BackendFFmpegFallback}
	resolver := NewRenderBackendResolver(registry)

	wmPlan := requirementsPlan("", &PlanWatermark{Position: PositionTopRight, Opacity: 1}, nil)
	if _, err := resolver.Resolve(context.Background(), wmPlan, RendererCapabilities{}); !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable for unsupported requirements, got %v", err)
	}
}

func TestResolveBackendFailsClosedWhenProbeMissingOrFails(t *testing.T) {
	resolver := NewRenderBackendResolver(nil)
	plan := ClipRenderPlanV1{}

	// nil probe → error, not silent fallback.
	_, err := ResolveBackend(context.Background(), nil, resolver, plan)
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("nil probe: expected ErrBackendUnavailable, got %v", err)
	}

	// nil resolver → error, not silent fallback.
	_, err = ResolveBackend(context.Background(), &emptyProbe{}, nil, plan)
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("nil resolver: expected ErrBackendUnavailable, got %v", err)
	}

	// failing probe → wrapped error, not silent fallback.
	_, err = ResolveBackend(context.Background(), failingProbe{}, resolver, plan)
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("failing probe: expected ErrBackendUnavailable, got %v", err)
	}
}

func TestResolveBackendResolvesToFFmpegWithEmptyProbedCapabilities(t *testing.T) {
	resolver := NewRenderBackendResolver(nil)
	plan := ClipRenderPlanV1{}

	// Explicit probe that reports empty capabilities → FFmpeg fallback is the
	// intentional choice (CPU-only worker, not a degrading wiring bug).
	backend, err := ResolveBackend(context.Background(), emptyProbe{}, resolver, plan)
	if err != nil {
		t.Fatalf("empty probe: %v", err)
	}
	if backend != BackendFFmpegFallback {
		t.Fatalf("empty probe: got %q, want ffmpeg_fallback", backend)
	}
}

type failingProbe struct{}

func (failingProbe) ProbeCapabilities(context.Context) (RendererCapabilities, error) {
	return RendererCapabilities{}, errors.New("ffmpeg unavailable")
}

type emptyProbe struct{}

func (emptyProbe) ProbeCapabilities(context.Context) (RendererCapabilities, error) {
	return RendererCapabilities{}, nil
}
