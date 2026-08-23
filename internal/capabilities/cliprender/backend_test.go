package cliprender

import (
	"context"
	"errors"
	"testing"
)

func TestRendererCapabilitiesSatisfies(t *testing.T) {
	full := RendererCapabilities{
		NVDEC: true, NVENCH264: true, NVENCHEVC: true,
		GPUScale: true, GPUBlur: true, GPUAlpha: true, SubtitleTexture: true,
	}
	if !full.Satisfies(RendererCapabilities{NVDEC: true, NVENCH264: true, GPUScale: true, GPUBlur: true, GPUAlpha: true}) {
		t.Fatal("full capabilities must satisfy the cuda-native requirements")
	}
	if !full.Satisfies(RendererCapabilities{}) {
		t.Fatal("empty requirements must be satisfied by any capabilities")
	}

	partial := RendererCapabilities{NVDEC: true, NVENCH264: true}
	if partial.Satisfies(RendererCapabilities{GPUScale: true}) {
		t.Fatal("missing gpu_scale must fail satisfaction")
	}
	if !partial.Satisfies(RendererCapabilities{NVDEC: true}) {
		t.Fatal("present nvdec must satisfy a requirement for nvdec")
	}
}

func TestNewRenderBackendRegistrySeedsCanonicalBackends(t *testing.T) {
	registry := NewRenderBackendRegistry()
	backends := registry.Backends()
	if len(backends) != 3 {
		t.Fatalf("backends = %v, want [cuda_native chronon_vulkan ffmpeg_fallback]", backends)
	}
	if backends[0] != BackendCudaNative || backends[1] != BackendChrononVulkan || backends[2] != BackendFFmpegFallback {
		t.Fatalf("backends order = %v, want cuda_native preferred", backends)
	}
	// ffmpeg fallback runs on an empty host.
	if !registry.CanRun(BackendFFmpegFallback, RendererCapabilities{}) {
		t.Fatal("ffmpeg fallback must run on empty capabilities")
	}
	// cuda native requires the full chain.
	if registry.CanRun(BackendCudaNative, RendererCapabilities{}) {
		t.Fatal("cuda native must not run on empty capabilities")
	}
	// Unknown backend never runs.
	if registry.CanRun(RenderBackend("bogus"), RendererCapabilities{}) {
		t.Fatal("unknown backend must not be runnable")
	}
}

func TestRenderBackendResolverPrefersCudaThenFallsBack(t *testing.T) {
	resolver := NewRenderBackendResolver(nil)

	cudaCaps := RendererCapabilities{
		NVDEC: true, NVENCH264: true, GPUScale: true, GPUBlur: true, GPUAlpha: true,
	}
	backend, err := resolver.Resolve(context.Background(), ClipRenderPlanV1{}, cudaCaps)
	if err != nil {
		t.Fatalf("Resolve(cuda caps): %v", err)
	}
	if backend != BackendCudaNative {
		t.Fatalf("backend = %q, want cuda_native", backend)
	}

	backend, err = resolver.Resolve(context.Background(), ClipRenderPlanV1{}, RendererCapabilities{NVDEC: true})
	if err != nil {
		t.Fatalf("Resolve(partial caps): %v", err)
	}
	if backend != BackendFFmpegFallback {
		t.Fatalf("backend = %q, want ffmpeg_fallback", backend)
	}
}

func TestRenderBackendResolverFailClosedWhenNoBackendRuns(t *testing.T) {
	registry := NewRenderBackendRegistry()
	// Remove the software fallback so only the GPU backend remains.
	registry.capabilities = map[RenderBackend]RendererCapabilities{
		BackendCudaNative: {NVDEC: true, NVENCH264: true, GPUScale: true, GPUBlur: true, GPUAlpha: true},
	}
	registry.order = []RenderBackend{BackendCudaNative}
	resolver := NewRenderBackendResolver(registry)

	_, err := resolver.Resolve(context.Background(), ClipRenderPlanV1{}, RendererCapabilities{})
	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("expected ErrBackendUnavailable, got %v", err)
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
