package cliprender

// backend.go owns the render backend CONTRACT.
//
// After the backend-authority demolition there is exactly ONE render backend:
// Chronon, executed by RenderingGen behind the shared queue boundary.
// PipelineGen describes WHAT it wants rendered (the sealed ClipRenderPlanV1:
// source/background/watermark/subtitles/output/audio); RenderingGen alone
// decides HOW to lower and execute it (asset resolution, semantic plan
// lowering, backend selection, Chronon execution).
//
// The former pipeline-side selection machinery was removed with the audit:
//
//	RenderBackendRegistry / RenderRequirementResolver / BackendSupport /
//	RenderBackendResolver / ResolveBackend / BackendCapabilityProbe /
//	BackendFFmpegFallback
//
// It was an illusion of pluggability: the worker unconditionally enforced
// `outcome.Backend == chronon_vulkan`, so the registry could never select a
// second backend and the resolver had no decision to make. A future second
// backend, if one is ever needed, must be resolved in its true owner —
// RenderingGen — never by reintroducing a one-value registry here.

import "errors"

// RenderBackend identifies the execution backend that rendered a sealed plan.
// It is a REPORTED identity, not a selector: the worker validates the
// artifact's certified backend against the Chronon contract and fails closed
// on any other value.
type RenderBackend string

// BackendChrononVulkan is the ONLY permitted render backend: the Chronon3d GPU
// compositor (NVDEC → CUDA/Vulkan surface → render graph → NVENC) executed by
// RenderingGen behind the shared queue boundary.
const BackendChrononVulkan RenderBackend = "chronon_vulkan"

// IsGPUBackend reports whether the backend executes on GPU hardware. Chronon
// is the only render backend and is GPU-native, so it is the single authority
// of "GPU-ness" for the worker's ExecutionSpec.RequireGPU gate.
func (b RenderBackend) IsGPUBackend() bool {
	return b == BackendChrononVulkan
}

// ErrBackendUnavailable is returned when no render boundary is available
// (unconfigured queue, failed submission). Fail-closed: a render is never
// silently downgraded or served by a local fallback.
var ErrBackendUnavailable = errors.New("clip.render: no render backend available")
