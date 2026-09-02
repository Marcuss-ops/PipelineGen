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
	"strings"
)

// RenderBackend identifies the execution backend that renders a sealed plan.
// The plan never carries a backend; the resolver selects one at execution
// time from host capabilities plus plan requirements.
type RenderBackend string

const (
	// BackendChrononVulkan is the PRIMARY backend: the Chronon3d GPU
	// compositor (NVDEC → CUDA/Vulkan surface → render graph → NVENC). It is
	// selected whenever the host certification gate passes
	// (ChrononNativeCertified) — it supports every plan requirement, so a
	// certified host renders through Chronon exclusively.
	BackendChrononVulkan RenderBackend = "chronon_vulkan"

	// BackendCudaNative is the PATH B hybrid (FFmpeg filter graph): NVDEC
	// decode → CUDA base video (scale_cuda, device-local) → CPU-rasterized
	// overlay layer (drawtext/subtitles/image on a transparent canvas) →
	// hwupload_cuda → overlay_cuda → NVENC (pix_fmt cuda). The base video
	// NEVER leaves VRAM (zero readback): only the small overlay layer crosses
	// the PCIe bus. It is the GPU path for hosts where the Chronon
	// certification gate refuses but the NVDEC/NVENC chain is present, and
	// only for the plans it can render device-local: overlays on a scale-100
	// base, no background plate, no style shadow/transition (those do not
	// travel in the Rust plan).
	BackendCudaNative RenderBackend = "cuda_native"

	// BackendFFmpegFallback is the single-pass FFmpeg filter graph (CPU
	// compositing) that Rust executes. It is the software baseline — always
	// runnable when ffmpeg is present — and the last-resort backend for hosts
	// without a certified Chronon and without a usable CUDA chain.
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
// distinct stage of the GPU pipeline; the CUDA native (PATH B) backend
// requires its chain, while the FFmpeg fallback requires none of it.
//
// ChrononVulkan reports that the Chronon render binary is CONFIGURED (the
// binary exists); ChrononNativeCertified reports that the binary was
// actually CERTIFIED — a real ~30-frame NVDEC → CUDA/Vulkan → NVENC job ran
// end-to-end on this exact host without faulting (e.g. the historical
// CUDA_ERROR_ILLEGAL_ADDRESS handoff bug). The registry gates the
// chronon_vulkan backend on ChrononNativeCertified, never on binary
// presence: a broken handoff must not add latency to every render.
type RendererCapabilities struct {
	NVDEC                  bool `json:"nvdec"`
	NVENCH264              bool `json:"nvenc_h264"`
	NVENCHEVC              bool `json:"nvenc_hevc"`
	GPUScale               bool `json:"gpu_scale"`
	GPUBlur                bool `json:"gpu_blur"`
	GPUAlpha               bool `json:"gpu_alpha"`
	SubtitleTexture        bool `json:"subtitle_texture"`
	ChrononVulkan          bool `json:"chronon_vulkan"`
	ChrononNativeCertified bool `json:"chronon_native_certified"`
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
	if required.ChrononNativeCertified && !c.ChrononNativeCertified {
		return false
	}
	return true
}

// RenderRequirements is the per-plan execution requirement set derived from
// the sealed plan. It describes WHAT the plan needs, independent of any
// backend — the registry's BackendSupport declares which backend can satisfy
// each requirement, and the resolver compares the two (plus the probed host
// capabilities) to select ONE backend. No component re-derives requirements
// inline; RenderRequirementResolver is the single owner.
type RenderRequirements struct {
	NeedsBackground     bool `json:"needs_background"`
	NeedsBlurBackground bool `json:"needs_blur_background"`
	NeedsImageWatermark bool `json:"needs_image_watermark"`
	NeedsTextWatermark  bool `json:"needs_text_watermark"`
	NeedsShadow         bool `json:"needs_shadow"`
	NeedsTransition     bool `json:"needs_transition"`
	NeedsBurnSubtitles  bool `json:"needs_burn_subtitles"`
	NeedsGPUAlpha       bool `json:"needs_gpu_alpha"`
	NeedsScale          bool `json:"needs_scale"`
}

// RenderRequirementResolver derives the plan's execution requirements. It is
// pure: reads only the sealed plan, never host state.
type RenderRequirementResolver struct{}

// Resolve maps a sealed plan onto its requirement set. NeedsGPUAlpha is an
// aggregate: any overlay/burn layer over the video surface requires alpha
// compositing.
func (RenderRequirementResolver) Resolve(plan ClipRenderPlanV1) RenderRequirements {
	reqs := RenderRequirements{}
	if plan.Background != nil {
		reqs.NeedsBackground = plan.Background.Mode != BackgroundModeNone
		reqs.NeedsBlurBackground = plan.Background.Mode == BackgroundModeBlurSource
	}
	if plan.Watermark != nil {
		if strings.TrimSpace(plan.Watermark.Text) != "" {
			reqs.NeedsTextWatermark = true
		} else {
			reqs.NeedsImageWatermark = true
		}
		if plan.Watermark.Style != nil {
			reqs.NeedsShadow = reqs.NeedsShadow || plan.Watermark.Style.Shadow != nil
			reqs.NeedsTransition = reqs.NeedsTransition || plan.Watermark.Style.TransitionIn != nil
		}
	}
	if plan.Subtitles != nil {
		reqs.NeedsBurnSubtitles = plan.Subtitles.Mode == SubtitlesModeBurn
		if plan.Subtitles.Style != nil {
			reqs.NeedsShadow = reqs.NeedsShadow || plan.Subtitles.Style.Shadow != nil
			reqs.NeedsTransition = reqs.NeedsTransition || plan.Subtitles.Style.TransitionIn != nil
		}
	}
	reqs.NeedsGPUAlpha = reqs.NeedsBackground || reqs.NeedsImageWatermark ||
		reqs.NeedsTextWatermark || reqs.NeedsBurnSubtitles
	// Foreground fit/pad (scale != 100) requires a pad filter that has no
	// device-local CUDA equivalent — the CUDA hybrid cannot do it without a
	// readback, so it is a hard requirement the backend must declare. Zero is
	// the unset sentinel normalized to 100 by Compile.
	reqs.NeedsScale = plan.Output.ForegroundScalePercent != 0 &&
		plan.Output.ForegroundScalePercent != 100
	return reqs
}

// BackendSupport declares the plan requirements a backend can satisfy. A
// false field means the backend cannot fulfill that requirement — the
// resolver never selects it for plans that need it.
type BackendSupport struct {
	Background     bool `json:"background"`
	BlurBackground bool `json:"blur_background"`
	ImageWatermark bool `json:"image_watermark"`
	TextWatermark  bool `json:"text_watermark"`
	Shadow         bool `json:"shadow"`
	Transition     bool `json:"transition"`
	BurnSubtitles  bool `json:"burn_subtitles"`
	GPUAlpha       bool `json:"gpu_alpha"`
	Scale          bool `json:"scale"`
}

// Satisfies reports whether the backend supports every required plan
// requirement (a false requirement is trivially satisfied).
func (s BackendSupport) Satisfies(reqs RenderRequirements) bool {
	if reqs.NeedsBackground && !s.Background {
		return false
	}
	if reqs.NeedsBlurBackground && !s.BlurBackground {
		return false
	}
	if reqs.NeedsImageWatermark && !s.ImageWatermark {
		return false
	}
	if reqs.NeedsTextWatermark && !s.TextWatermark {
		return false
	}
	if reqs.NeedsShadow && !s.Shadow {
		return false
	}
	if reqs.NeedsTransition && !s.Transition {
		return false
	}
	if reqs.NeedsBurnSubtitles && !s.BurnSubtitles {
		return false
	}
	if reqs.NeedsGPUAlpha && !s.GPUAlpha {
		return false
	}
	if reqs.NeedsScale && !s.Scale {
		return false
	}
	return true
}

// RenderBackendRegistry maps each backend to the host capabilities it
// REQUIRES and the plan requirements it can SATISFY. It is the single owner
// of what a backend can do, so the resolver never encodes a backend's
// requirements inline. Registration order is resolution preference order
// (most capable first).
type RenderBackendRegistry struct {
	capabilities map[RenderBackend]RendererCapabilities
	supports     map[RenderBackend]BackendSupport
	order        []RenderBackend
}

// NewRenderBackendRegistry seeds the canonical backends in resolution
// preference order: the Chronon Vulkan compositor first (the PRIMARY GPU
// backend — selected whenever the certification gate passes, since it
// supports every plan requirement), then the PATH B CUDA hybrid (GPU for
// hosts where the Chronon gate refuses but the NVDEC/NVENC chain is
// present), then the software FFmpeg fallback (always runnable).
func NewRenderBackendRegistry() *RenderBackendRegistry {
	registry := &RenderBackendRegistry{
		capabilities: make(map[RenderBackend]RendererCapabilities),
		supports:     make(map[RenderBackend]BackendSupport),
	}
	// The Chronon backend is gated on a REAL certification, not on binary
	// presence: the probe decorator must have run a short NVDEC → CUDA/Vulkan
	// → NVENC job successfully on this host (ChrononNativeCertified). A
	// configured-but-uncertified binary is not selectable — a broken handoff
	// (e.g. CUDA_ERROR_ILLEGAL_ADDRESS) must not add latency to every render.
	// ChrononVulkan (binary configured) is required too so a bare certified
	// flag without a binary is never trusted (the certifier only sets the
	// flag after a successful binary run; the requirement is structural).
	registry.Register(BackendChrononVulkan, RendererCapabilities{
		ChrononVulkan:          true,
		ChrononNativeCertified: true,
	})
	registry.SetSupport(BackendChrononVulkan, BackendSupport{
		Background:     true,
		BlurBackground: true,
		ImageWatermark: true,
		TextWatermark:  true,
		Shadow:         true,
		Transition:     true,
		BurnSubtitles:  false,
		GPUAlpha:       true,
		Scale:          true,
	})
	// PATH B CUDA hybrid: requires the NVDEC/NVENC/GPU chain and supports
	// only the plans it can render device-local (zero readback of the base
	// video): image overlays on a scale-100
	// base, no background plate, no style shadow/transition (they do not
	// travel in the Rust plan). Everything else routes to Chronon or to the
	// software baseline.
	registry.Register(BackendCudaNative, RendererCapabilities{
		NVDEC:     true,
		NVENCH264: true,
		GPUScale:  true,
		GPUAlpha:  true,
	})
	registry.SetSupport(BackendCudaNative, BackendSupport{
		ImageWatermark: true,
		GPUAlpha:       true,
	})
	registry.Register(BackendFFmpegFallback, RendererCapabilities{})
	registry.SetSupport(BackendFFmpegFallback, BackendSupport{
		Background:     true,
		BlurBackground: true,
		ImageWatermark: true,
		TextWatermark:  true,
		Shadow:         true,
		Transition:     true,
		BurnSubtitles:  true,
		GPUAlpha:       true,
		Scale:          true,
	})
	return registry
}

// Register records a backend and its required host capabilities. Registering
// an already-known backend replaces its requirements and keeps its original
// resolution preference slot.
func (r *RenderBackendRegistry) Register(backend RenderBackend, required RendererCapabilities) {
	if _, exists := r.capabilities[backend]; !exists {
		r.order = append(r.order, backend)
	}
	r.capabilities[backend] = required
}

// SetSupport declares the plan requirements the backend can satisfy.
func (r *RenderBackendRegistry) SetSupport(backend RenderBackend, support BackendSupport) {
	r.supports[backend] = support
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

// SupportsPlan reports whether the backend can satisfy the plan's
// requirements. A backend without a support declaration can satisfy nothing
// (fail-closed).
func (r *RenderBackendRegistry) SupportsPlan(backend RenderBackend, reqs RenderRequirements) bool {
	support, ok := r.supports[backend]
	if !ok {
		return false
	}
	return support.Satisfies(reqs)
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

// Resolve picks the first registered backend that (a) the host can run and
// (b) can satisfy the plan's requirements (registration order = preference
// order). It is the SINGLE authority of backend selection: the plan's
// requirements are derived here (RenderRequirementResolver), never re-derived
// by an adapter. Fail-closed: when no registered backend qualifies, a typed
// error is returned — no silent downgrade past the registry's declarations.
func (r *RenderBackendResolver) Resolve(_ context.Context, plan ClipRenderPlanV1, capabilities RendererCapabilities) (RenderBackend, error) {
	reqs := (RenderRequirementResolver{}).Resolve(plan)
	for _, backend := range r.registry.Backends() {
		if r.registry.CanRun(backend, capabilities) && r.registry.SupportsPlan(backend, reqs) {
			return backend, nil
		}
	}
	return "", fmt.Errorf(
		"%w: host capabilities %+v satisfy no registered backend for plan requirements %+v",
		ErrBackendUnavailable,
		capabilities,
		reqs,
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
