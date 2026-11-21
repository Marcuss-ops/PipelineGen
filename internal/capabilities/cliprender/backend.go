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

import (
	"errors"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// RenderBackend identifies the execution backend that rendered a sealed plan.
// The type and its only value live in the kernel so the PostgreSQL cache
// adapter can implement the port without importing this capability (RA-11).
type RenderBackend = kernelmedia.RenderBackend

// BackendChrononVulkan is the ONLY permitted render backend.
const BackendChrononVulkan = kernelmedia.BackendChrononVulkan

// ErrBackendUnavailable is returned when no render boundary is available
// (unconfigured queue, failed submission). Fail-closed: a render is never
// silently downgraded or served by a local fallback.
var ErrBackendUnavailable = errors.New("clip.render: no render backend available")
