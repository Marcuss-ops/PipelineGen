package cliprender

// backend.go owns render backend selection: the backend identity, the probed
// host capabilities, and the registry/resolver that map a sealed plan onto
// the most capable backend the host can actually run.
//
// There is deliberately NO hardcoded `if NVIDIA { CUDA }` anywhere in the
// capability (or on the Rust boundary): the resolver compares the probed host
// capabilities against each registered backend's REQUIRED capabilities and
// returns the first backend the host can satisfy, preferring the GPU native
// compositor over the software fallback. The sealed ClipRenderPlanV1 keeps
// describing what to render (source/background/watermark/subtitles/output/
// audio); the backend only changes HOW those blocks are applied.

import (
	"context"
	"errors"
	"fmt"
)

// RenderBackend identifies the execution backend that renders a sealed plan.
// The plan never carries a backend; the resolver selects one at execution
// time from host capabilities (plus plan requirements, when those land).
type RenderBackend string

const (
	// BackendChrononVulkan delegates complex compositing/text to Chronon3d.
	// Rust remains responsible for acquisition, probing and delivery; Chronon
	// owns the GPU render graph and persistent glyph atlas.
	BackendChrononVulkan RenderBackend = "chronon_vulkan"

	// BackendCudaNative is the full-GPU compositor: NVDEC → GPU transform →
	// VRAM alpha blend → NV12 → NVENC. Video frames never leave VRAM.
	BackendCudaNative RenderBackend = "cuda_native"

	// BackendFFmpegFallback is the legacy single-pass FFmpeg filter graph
	// (CPU compositing). It is the software baseline and is always runnable
	// when ffmpeg is present.
	BackendFFmpegFallback RenderBackend = "ffmpeg_fallback"
)

// IsValid reports whether the backend is a known identifier.
func (b RenderBackend) IsValid() bool {
	return b == BackendChrononVulkan || b == BackendCudaNative || b == BackendFFmpegFallback
}

// ErrBackendUnavailable is returned when no registered backend can run on
// the host. Fail-closed: a render is never silently downgraded past what the
// registry declares runnable.
var ErrBackendUnavailable = errors.New("clip.render: no render backend available")

// RendererCapabilities is the probed host capability set. Each flag names a
// distinct stage of the GPU pipeline; the CUDA native backend requires the
// whole chain, while the FFmpeg fallback requires none of it.
type RendererCapabilities struct {
	NVDEC           bool `json:"nvdec"`
	NVENCH264       bool `json:"nvenc_h264"`
	NVENCHEVC       bool `json:"nvenc_hevc"`
	GPUScale        bool `json:"gpu_scale"`
	GPUBlur         bool `json:"gpu_blur"`
	GPUAlpha        bool `json:"gpu_alpha"`
	SubtitleTexture bool `json:"subtitle_texture"`
	ChrononVulkan   bool `json:"chronon_vulkan"`
}

// Satisfies reports whether the host capabilities satisfy every required
// capability (a false requirement is trivially satisfied).
func (c RendererCapabilities) Satisfies(required RendererCapabilities) bool {
	if required.NVDEC && !c.NVDEC {
		return false
	}
	if required.NVENCH264 && !c.NVENCH264 {
		return false
	}
	if required.NVENCHEVC && !c.NVENCHEVC {
		return false
	}
	if required.GPUScale && !c.GPUScale {
		return false
	}
	if required.GPUBlur && !c.GPUBlur {
		return false
	}
	if required.GPUAlpha && !c.GPUAlpha {
		return false
	}
	if required.SubtitleTexture && !c.SubtitleTexture {
		return false
	}
	if required.ChrononVulkan && !c.ChrononVulkan {
		return false
	}
	return true
}

// RenderBackendRegistry maps each backend to the capabilities it REQUIRES.
// It is the single owner of what a backend can do, so the resolver never
// encodes a backend's requirements inline. Registration order is resolution
// preference order (most capable first).
type RenderBackendRegistry struct {
	capabilities map[RenderBackend]RendererCapabilities
	order        []RenderBackend
}

// NewRenderBackendRegistry seeds the canonical backends: the CUDA native
// compositor (preferred) followed by the software FFmpeg fallback.
func NewRenderBackendRegistry() *RenderBackendRegistry {
	registry := &RenderBackendRegistry{
		capabilities: make(map[RenderBackend]RendererCapabilities),
	}
	registry.Register(BackendCudaNative, RendererCapabilities{
		NVDEC:     true,
		NVENCH264: true,
		GPUScale:  true,
		GPUAlpha:  true,
	})
	registry.Register(BackendChrononVulkan, RendererCapabilities{ChrononVulkan: true})
	registry.Register(BackendFFmpegFallback, RendererCapabilities{})
	return registry
}

// Register records a backend and its required capabilities. Registering an
// already-known backend replaces its requirements and keeps its original
// resolution preference slot.
func (r *RenderBackendRegistry) Register(backend RenderBackend, required RendererCapabilities) {
	if _, exists := r.capabilities[backend]; !exists {
		r.order = append(r.order, backend)
	}
	r.capabilities[backend] = required
}

// Backends returns the registered backends in resolution-preference order.
func (r *RenderBackendRegistry) Backends() []RenderBackend {
	out := make([]RenderBackend, len(r.order))
	copy(out, r.order)
	return out
}

// CanRun reports whether the host capabilities satisfy the backend's
// required capabilities. An unknown backend is never runnable (fail-closed).
func (r *RenderBackendRegistry) CanRun(backend RenderBackend, host RendererCapabilities) bool {
	required, ok := r.capabilities[backend]
	if !ok {
		return false
	}
	return host.Satisfies(required)
}

// BackendCapabilityProbe detects the host's render capabilities. The concrete
// implementation is wired at the composition root (it runs the canonical
// ffmpeg binary); the capability never imports infrastructure.
type BackendCapabilityProbe interface {
	ProbeCapabilities(ctx context.Context) (RendererCapabilities, error)
}

// RenderBackendResolver resolves the backend for a sealed plan from the
// probed host capabilities. It is pure: it reads the registry + capabilities,
// never ffmpeg, never hardware directly.
type RenderBackendResolver struct {
	registry *RenderBackendRegistry
}

// NewRenderBackendResolver constructs a resolver over the given registry.
// A nil registry falls back to the canonical seeded registry.
func NewRenderBackendResolver(registry *RenderBackendRegistry) *RenderBackendResolver {
	if registry == nil {
		registry = NewRenderBackendRegistry()
	}
	return &RenderBackendResolver{registry: registry}
}

// Resolve picks the first registered backend the host can run (registration
// order = preference order). The plan is accepted for forward compatibility:
// plan-driven requirements (e.g. subtitle_texture when burn subtitles are
// present) land alongside the native compositor and are consulted here.
// Fail-closed: when no registered backend can run, a typed error is returned.
func (r *RenderBackendResolver) Resolve(_ context.Context, _ ClipRenderPlanV1, capabilities RendererCapabilities) (RenderBackend, error) {
	for _, backend := range r.registry.Backends() {
		if r.registry.CanRun(backend, capabilities) {
			return backend, nil
		}
	}
	return "", fmt.Errorf(
		"%w: host capabilities %+v satisfy no registered backend",
		ErrBackendUnavailable,
		capabilities,
	)
}

// ResolveBackend probes host capabilities and resolves the backend through
// the resolver. A missing probe/resolver, or a failed probe, is a typed
// error (ErrBackendUnavailable) — the worker is DEGRADED / NOT READY when
// it cannot probe host capabilities, never silently falling back to a
// software path that may hide a broken wiring. A caller that explicitly
// expects the FFmpeg fallback (e.g. a CPU-only worker) must wire a probe
// that reports empty capabilities so the intent is explicit.
func ResolveBackend(
	ctx context.Context,
	probe BackendCapabilityProbe,
	resolver *RenderBackendResolver,
	plan ClipRenderPlanV1,
) (RenderBackend, error) {
	if probe == nil {
		return "", fmt.Errorf("%w: backend capability probe is not wired (DEGRADED — no host capability information)", ErrBackendUnavailable)
	}
	if resolver == nil {
		return "", fmt.Errorf("%w: render backend resolver is not wired (NOT READY — cannot select a backend)", ErrBackendUnavailable)
	}
	capabilities, err := probe.ProbeCapabilities(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: capability probe failed: %w (DEGRADED — host capabilities unknown)", ErrBackendUnavailable, err)
	}
	return resolver.Resolve(ctx, plan, capabilities)
}
