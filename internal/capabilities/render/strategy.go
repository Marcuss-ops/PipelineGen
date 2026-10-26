// Package render — strategy.go owns the render execution strategy: the
// deterministic decision of HOW a sealed RenderPlan is executed. It is the
// single decision surface shared by the executor, the artifact cache, the
// checkpoint registry and the performance recorder:
//
//	RenderPlan
//	  ↓
//	artifact cache probe (policy + cache wired)
//	  ├── HIT  → CACHE_HIT (reuse, zero render)
//	  └── MISS ↓
//	probe every manifest source
//	  ├── copy compatible → STREAM_COPY (zero re-encode)
//	  └── otherwise       → FULL_RENDER
//
// The resolver is conservative by construction: any fact that cannot be
// verified (unprobed asset, unknown GOP, missing policy) blocks stream copy
// and falls back to FULL_RENDER, never to an unverified copy.
package render

import (
	"context"

	capcache "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifactcache"
)

// CacheOperationRenderFinal is the artifact-cache operation namespace for a
// whole-plan final render. Scene-level renders will use their own operation
// ("render_scene") with the scene fingerprint as SourceSHA256.
const CacheOperationRenderFinal = "render_final"

// ExecutionMode is the canonical execution decision for a RenderPlan.
type ExecutionMode string

const (
	// ExecutionCache reuses an existing certified artifact: zero render.
	ExecutionCache ExecutionMode = "CACHE_HIT"
	// ExecutionCopy concatenates certified sources with stream copy: zero
	// decode, zero encode, zero compositing.
	ExecutionCopy ExecutionMode = "STREAM_COPY"
	// ExecutionRender re-encodes the sources through the renderer.
	ExecutionRender ExecutionMode = "FULL_RENDER"
)

// CompatibilityReport is the full decision record of one Resolve call: the
// chosen mode, per-dimension compatibility facts, and the human-readable
// reasons behind the decision. It feeds the performance recorder (strategy
// column) and checkpoint/replay bookkeeping.
type CompatibilityReport struct {
	Mode            ExecutionMode
	VideoCompatible bool
	AudioCompatible bool

	RequiresScale       bool
	RequiresFPSChange   bool
	RequiresPixelChange bool
	RequiresOverlay     bool
	RequiresTransition  bool
	RequiresWatermark   bool
	RequiresAudioMix    bool

	Reasons []string
}

// TargetProfile is the fully-resolved output profile the rendered artifact
// must satisfy. It is the concrete counterpart of the policy's
// TargetProfileHash: the resolver compares probed source facts against it.
// An incomplete profile fails closed (never copy-eligible).
type TargetProfile struct {
	Codec            string
	PixelFormat      string
	Width            int
	Height           int
	KeyframeInterval int
}

// AssetFacts is the capability-owned projection of one probed source
// asset's media facts. KeyframeInterval is 0 when the probe cannot verify
// GOP structure — an unverified GOP blocks stream copy (fail closed).
type AssetFacts struct {
	VideoCodec       string
	PixelFormat      string
	Width            int
	Height           int
	FPS              float64
	FPSNum           int
	FPSDen           int
	KeyframeInterval int
}

// AssetProber is the narrow port that returns real media facts about a
// manifest asset. The concrete adapter reads the actual bytes (Rust probe);
// the capability never trusts manifest metadata for the copy decision.
type AssetProber interface {
	Probe(ctx context.Context, path string) (AssetFacts, error)
}

// StrategyResolver decides how a sealed RenderPlan must be executed.
type StrategyResolver interface {
	Resolve(ctx context.Context, plan RenderPlan) (CompatibilityReport, error)
}

// SmartResolver is the canonical StrategyResolver. It is constructed with
// the target profile and the optional artifact cache; a nil cache disables
// the CACHE_HIT path (the probe + copy/render decision still runs).
type SmartResolver struct {
	probe  AssetProber
	cache  capcache.Cache
	target TargetProfile
}

// NewSmartResolver builds the resolver. Fail-closed: a nil prober yields a
// resolver that can never verify copy eligibility (always FULL_RENDER with
// a reason); a nil cache simply disables cache probing.

// Resolve decides the execution mode for the sealed plan:
//
//  1. cache probe first (policy + cache wired): hit → CACHE_HIT;
//  2. otherwise probe every manifest source: all copy-eligible → STREAM_COPY;
//  3. otherwise FULL_RENDER.
//
// A structurally invalid plan is a hard error (no decision is possible on a
// drifted plan). Probe failures never fail the decision: they degrade to
// FULL_RENDER with a reason, because rendering does not depend on the probe.

// cacheHit probes the artifact cache with the canonical final-render key
// (SourceSHA256 = PlanSHA256, operation = render_final, parameters = policy
// hashes, processor = renderer version). Returns the cache digest on hit so
// callers can open the artifact.

// FinalRenderCacheKey builds the canonical artifact-cache key for executing
// a sealed plan: SourceSHA256 is the plan's own PlanSHA256 (which already
// contains the execution policy), parameters carry the policy hashes, and
// the processor version is the policy's renderer version. A plan without a
// policy has no cache identity.

// videoCompatibility is one asset's per-dimension verdict against the
// target profile and plan frame rate. reason is "" exactly when the asset
// is stream-copy eligible; the flags record which dimensions would need
// re-encoding (they explain a FULL_RENDER decision).
type videoCompatibility struct {
	reason              string
	requiresScale       bool
	requiresFPSChange   bool
	requiresPixelChange bool
}

// fpsCompatible compares the probed fps against the plan's rational frame
// rate: exact rational match when the probe reports num/den, float tolerance
// otherwise (mirror of the clip contract's 0.5 fps tolerance).

// targetComplete reports whether the pinned target profile is fully
// specified. An incomplete profile can never certify a copy.

// planHasOverlays reports whether the plan carries any overlay surface.
// Today's canonical RenderPlan cannot express overlays; the hook exists so
// the moment a plan carries them, stream copy is gated here — the single
// decision surface.
