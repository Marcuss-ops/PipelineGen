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
	"encoding/json"
	"fmt"
	"math"
	"strings"

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
func NewSmartResolver(probe AssetProber, cache capcache.Cache, target TargetProfile) *SmartResolver {
	return &SmartResolver{probe: probe, cache: cache, target: target}
}

// Resolve decides the execution mode for the sealed plan:
//
//  1. cache probe first (policy + cache wired): hit → CACHE_HIT;
//  2. otherwise probe every manifest source: all copy-eligible → STREAM_COPY;
//  3. otherwise FULL_RENDER.
//
// A structurally invalid plan is a hard error (no decision is possible on a
// drifted plan). Probe failures never fail the decision: they degrade to
// FULL_RENDER with a reason, because rendering does not depend on the probe.
func (r *SmartResolver) Resolve(ctx context.Context, plan RenderPlan) (CompatibilityReport, error) {
	if err := plan.Validate(); err != nil {
		return CompatibilityReport{}, fmt.Errorf("render strategy: %w", err)
	}
	report := CompatibilityReport{Mode: ExecutionRender, VideoCompatible: false, AudioCompatible: true}
	policy := plan.ExecutionPolicy
	if policy == nil {
		report.Reasons = append(report.Reasons, "no execution policy: cache identity and stream copy are disabled")
	} else {
		if hit, digest, err := r.cacheHit(ctx, plan, policy); err == nil && hit {
			report.Mode = ExecutionCache
			report.VideoCompatible = true
			report.Reasons = append(report.Reasons, "artifact cache hit "+digest)
			return report, nil
		} else if err != nil {
			// A cache probe failure must never block rendering (mirror of the
			// sourcedl advisory-lookup contract): degrade to the probe path.
			report.Reasons = append(report.Reasons, "artifact cache probe failed: "+err.Error())
		}
		if !policy.AllowStreamCopy {
			report.Reasons = append(report.Reasons, "stream copy disabled by execution policy")
		}
	}

	// Compositing requirements from the plan surface. Today's canonical
	// RenderPlan cannot express overlays/transitions/watermarks, so these
	// stay false; the moment a plan carries them they gate stream copy here
	// (single decision surface, no scattered checks).
	report.RequiresOverlay = planHasOverlays(plan)
	report.RequiresTransition = planHasTransitions(plan)
	report.RequiresWatermark = planHasWatermark(plan)

	// Audio compatibility is a plan fact, not a probe fact: the final audio
	// is a certified asset muxed at assembly. Copy-eligible final audio (or
	// no audio at all) means no audio mix is required.
	if plan.FinalAudio != nil && !plan.FinalAudio.CopyEligible {
		report.AudioCompatible = false
		report.RequiresAudioMix = true
		report.Reasons = append(report.Reasons, "final audio is not copy-eligible: audio mix required")
	}

	if policy == nil || !policy.AllowStreamCopy || report.RequiresOverlay || report.RequiresTransition || report.RequiresWatermark {
		return report, nil
	}
	if !r.targetComplete() {
		report.Reasons = append(report.Reasons, "target profile is incomplete: stream copy cannot be verified")
		return report, nil
	}
	if r.probe == nil {
		report.Reasons = append(report.Reasons, "asset prober is not configured: stream copy cannot be verified")
		return report, nil
	}

	copyEligible := true
	for _, entry := range plan.Manifest {
		facts, err := r.probe.Probe(ctx, entry.Path)
		if err != nil {
			copyEligible = false
			report.Reasons = append(report.Reasons, fmt.Sprintf("asset %s: probe failed (%v): stream copy cannot be verified", entry.AssetID, err))
			continue
		}
		check := r.videoCompatibility(entry.AssetID, facts, plan)
		report.RequiresScale = report.RequiresScale || check.requiresScale
		report.RequiresFPSChange = report.RequiresFPSChange || check.requiresFPSChange
		report.RequiresPixelChange = report.RequiresPixelChange || check.requiresPixelChange
		if check.reason != "" {
			copyEligible = false
			report.Reasons = append(report.Reasons, check.reason)
		}
	}
	if copyEligible {
		report.Mode = ExecutionCopy
		report.VideoCompatible = true
		report.Reasons = append(report.Reasons, "all sources are stream-copy compatible; per-segment keyframe alignment is certified by the executor")
	}
	return report, nil
}

// cacheHit probes the artifact cache with the canonical final-render key
// (SourceSHA256 = PlanSHA256, operation = render_final, parameters = policy
// hashes, processor = renderer version). Returns the cache digest on hit so
// callers can open the artifact.
func (r *SmartResolver) cacheHit(ctx context.Context, plan RenderPlan, policy *RenderExecutionPolicy) (bool, string, error) {
	if r.cache == nil {
		return false, "", nil
	}
	key, err := FinalRenderCacheKey(plan)
	if err != nil {
		return false, "", err
	}
	digest, err := key.Digest()
	if err != nil {
		return false, "", err
	}
	entry, ok, err := r.cache.Lookup(ctx, key, 0)
	if err != nil {
		return false, "", err
	}
	return ok && entry != nil, digest, nil
}

// FinalRenderCacheKey builds the canonical artifact-cache key for executing
// a sealed plan: SourceSHA256 is the plan's own PlanSHA256 (which already
// contains the execution policy), parameters carry the policy hashes, and
// the processor version is the policy's renderer version. A plan without a
// policy has no cache identity.
func FinalRenderCacheKey(plan RenderPlan) (capcache.Key, error) {
	if plan.ExecutionPolicy == nil {
		return capcache.Key{}, fmt.Errorf("render strategy: execution policy is required for a cache key")
	}
	if err := validateExecutionPolicy(plan.ExecutionPolicy); err != nil {
		return capcache.Key{}, err
	}
	params, err := json.Marshal(struct {
		OutputProfile  string `json:"output_profile"`
		RendererPolicy string `json:"renderer_policy"`
	}{plan.ExecutionPolicy.TargetProfileHash, plan.ExecutionPolicy.EncoderPolicyHash})
	if err != nil {
		return capcache.Key{}, fmt.Errorf("render strategy: cache parameters: %w", err)
	}
	return capcache.Key{
		SourceSHA256:     plan.PlanSHA256,
		Operation:        CacheOperationRenderFinal,
		ParametersJSON:   string(params),
		ProcessorVersion: plan.ExecutionPolicy.RendererVersion,
	}, nil
}

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

func (r *SmartResolver) videoCompatibility(assetID string, facts AssetFacts, plan RenderPlan) videoCompatibility {
	if !strings.EqualFold(facts.VideoCodec, r.target.Codec) {
		return videoCompatibility{reason: fmt.Sprintf("asset %s: codec %q != target %q", assetID, facts.VideoCodec, r.target.Codec)}
	}
	if facts.Width != r.target.Width || facts.Height != r.target.Height {
		return videoCompatibility{reason: fmt.Sprintf("asset %s: geometry %dx%d != target %dx%d", assetID, facts.Width, facts.Height, r.target.Width, r.target.Height), requiresScale: true}
	}
	if !strings.EqualFold(facts.PixelFormat, r.target.PixelFormat) {
		return videoCompatibility{reason: fmt.Sprintf("asset %s: pixel format %q != target %q", assetID, facts.PixelFormat, r.target.PixelFormat), requiresPixelChange: true}
	}
	if !fpsCompatible(facts, plan) {
		return videoCompatibility{reason: fmt.Sprintf("asset %s: fps %.3f != plan %d/%d", assetID, facts.FPS, plan.FPSNumerator, plan.FPSDenominator), requiresFPSChange: true}
	}
	if r.target.KeyframeInterval > 0 && facts.KeyframeInterval != r.target.KeyframeInterval {
		if facts.KeyframeInterval <= 0 {
			return videoCompatibility{reason: fmt.Sprintf("asset %s: keyframe interval unverified by probe", assetID)}
		}
		return videoCompatibility{reason: fmt.Sprintf("asset %s: keyframe interval %d != target %d", assetID, facts.KeyframeInterval, r.target.KeyframeInterval)}
	}
	return videoCompatibility{}
}

// fpsCompatible compares the probed fps against the plan's rational frame
// rate: exact rational match when the probe reports num/den, float tolerance
// otherwise (mirror of the clip contract's 0.5 fps tolerance).
func fpsCompatible(facts AssetFacts, plan RenderPlan) bool {
	if facts.FPSNum > 0 && facts.FPSDen > 0 && plan.FPSNumerator > 0 && plan.FPSDenominator > 0 {
		return facts.FPSNum == int(plan.FPSNumerator) && facts.FPSDen == int(plan.FPSDenominator)
	}
	target := float64(plan.FPSNumerator) / float64(plan.FPSDenominator)
	return math.Abs(facts.FPS-target) <= 0.5
}

// targetComplete reports whether the pinned target profile is fully
// specified. An incomplete profile can never certify a copy.
func (r *SmartResolver) targetComplete() bool {
	return strings.TrimSpace(r.target.Codec) != "" &&
		strings.TrimSpace(r.target.PixelFormat) != "" &&
		r.target.Width > 0 && r.target.Height > 0
}

// planHasOverlays reports whether the plan carries any overlay surface.
// Today's canonical RenderPlan cannot express overlays; the hook exists so
// the moment a plan carries them, stream copy is gated here — the single
// decision surface.
func planHasOverlays(plan RenderPlan) bool { return false }

// planHasTransitions reports whether the plan carries any transition
// requiring compositing. See planHasOverlays.
func planHasTransitions(plan RenderPlan) bool { return false }

// planHasWatermark reports whether the plan carries a watermark. See
// planHasOverlays.
func planHasWatermark(plan RenderPlan) bool { return false }
