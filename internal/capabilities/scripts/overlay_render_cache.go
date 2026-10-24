package scriptgeneration

// overlay_render_cache.go owns the CONTENT-ADDRESSED reuse contract for
// per-item clips and fused batch compositions.
//
// A certified overlay clip is fully determined by its RenderKey (item content:
// template, text, params, asset digests, timing, canvas) plus the plan-level
// render context (background/source, animation policies, foreground scale,
// media contract) and the renderer build that produced it. A batch key hashes
// each member's render key and z-order into the same context. RenderingGen
// never needs the producing run identity: the same key names the same pixels.
//
// Before this file there was no index from RenderKey to the artifact it
// produced, so every retry, every rerun and every incremental golden run
// recompiled and re-rendered an identical clip. The cache stores only the
// certified locator (URL + SHA-256 + size) — the bytes already live in the
// object store under their content address — and the enqueuer treats a hit as
// a completed render, while still running this run's publication step.
//
// Failure posture: a cache is an optimization, never a dependency. A lookup
// error, a missing object behind a cached locator, or a disabled cache all
// degrade to a normal render. Only a certifiably wrong artifact is never
// served: every cached locator is validated (SHA-256 shape, http(s) URL,
// positive size) before it can become a RenderReference.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/buildinfo"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/rendercacheinfo"
)

// OverlayRenderCacheKillSwitch disables the cache when set to 0/false/no/off.
// Default is enabled: the cache can only avoid deterministic work, and every
// path degrades to a normal render.
const OverlayRenderCacheKillSwitch = rendercacheinfo.KillSwitchEnv

// OverlayRenderCacheEpochEnv overrides the deployment epoch mixed into every
// key. Operators bump it after a renderer upgrade that is not visible in the
// pipeline binary (for example a new Chronon build on the render worker).
const OverlayRenderCacheEpochEnv = "PIPELINEGEN_OVERLAY_CACHE_EPOCH"

// OverlayRenderCacheKey is the identity of one reusable overlay render. The
// fields are kept alongside the digest because a cache row must be inspectable
// by an operator without recomputing anything: RenderKey names the item,
// PlanContext names the canvas around it, Epoch names the renderer build.
type OverlayRenderCacheKey struct {
	RenderKey   string
	PlanContext string
	Epoch       string
}

// Validate reports whether the key is complete enough to address a cache row.
func (k OverlayRenderCacheKey) Validate() error {
	if len(k.RenderKey) != 64 || !isLowerHex(k.RenderKey) {
		return fmt.Errorf("overlay render cache key: render key %q is not a 64-hex digest", k.RenderKey)
	}
	if len(k.PlanContext) != 64 || !isLowerHex(k.PlanContext) {
		return fmt.Errorf("overlay render cache key: plan context %q is not a 64-hex digest", k.PlanContext)
	}
	if strings.TrimSpace(k.Epoch) == "" {
		return fmt.Errorf("overlay render cache key: epoch is required")
	}
	return nil
}

// Digest is the canonical cache identity: SHA-256 over the three fields with
// length-independent separators. The version tag keeps a future key shape from
// silently reusing rows written by this one.
func (k OverlayRenderCacheKey) Digest() string {
	return digest.SHA256String(strings.Join([]string{
		"overlay-render-cache/v1", k.RenderKey, k.PlanContext, k.Epoch,
	}, "\x00"))
}

func isLowerHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return s != ""
}

// OverlayRenderCache is the narrow persistence port. Lookup returns
// (nil, false, nil) for a miss; a transport/storage error must never be turned
// into a hit. Store is idempotent on the key digest. Invalidate is
// best-effort: a row that cannot be invalidated is reported to the caller,
// which logs it and continues with the render.
type OverlayRenderCache interface {
	Lookup(ctx context.Context, key OverlayRenderCacheKey) (*RenderArtifact, bool, error)
	Store(ctx context.Context, key OverlayRenderCacheKey, artifact RenderArtifact) error
	Invalidate(ctx context.Context, key OverlayRenderCacheKey, reason string) error
}

// OverlayArtifactProber verifies that a cached locator still resolves in the
// object store. The cache is an index, not a second copy: the bytes live in a
// store with its own retention policy, so a hit is only a hit when the object
// is still there.
type OverlayArtifactProber interface {
	Present(ctx context.Context, artifact RenderArtifact) (bool, error)
}

// OverlayRenderCacheEnabled reports the kill-switch state. Only an explicit
// 0/false/no/off disables it; an unset or empty value keeps the cache on.
func OverlayRenderCacheEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(OverlayRenderCacheKillSwitch))) {
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return true
	}
}

// OverlayRenderCacheEpoch returns the deployment epoch mixed into every key.
// Explicit override wins. Otherwise the pipeline build identity is used: a new
// binary cannot serve artifacts certified by an older one. Worker-side Chronon
// upgrades invisible to this binary are the operator's lever (the env var).
func OverlayRenderCacheEpoch() string {
	if epoch := strings.TrimSpace(os.Getenv(OverlayRenderCacheEpochEnv)); epoch != "" {
		return epoch
	}
	identity := buildinfo.Current()
	if identity.GitCommit != "" && identity.BuildTime != "" {
		return identity.GitCommit + "@" + identity.BuildTime
	}
	if identity.BinarySHA256 != "" {
		sha := identity.BinarySHA256
		if len(sha) > 16 {
			sha = sha[:16]
		}
		return "binary:" + sha
	}
	return "v1"
}

// overlayRenderCacheKeyForPlan derives a content-addressed cache identity for
// both individual items and fused compositions. A batch key includes every
// ordered item identity/render key plus the shared plan context, so a cached
// composition can never satisfy a different grouping or z-order.
func overlayRenderCacheKeyForPlan(plan capoverlay.OverlayPlan, epoch string) (OverlayRenderCacheKey, bool) {
	if len(plan.Items) == 0 {
		return OverlayRenderCacheKey{}, false
	}
	var renderKey string
	if len(plan.Items) == 1 {
		item := plan.Items[0]
		renderKey = strings.ToLower(strings.TrimSpace(item.RenderKey))
		if renderKey == "" {
			renderKey = capoverlay.ComputeRenderKey(plan, item)
		}
	} else {
		parts := []string{"overlay-render-batch/v1"}
		for index, item := range plan.Items {
			itemKey := strings.ToLower(strings.TrimSpace(item.RenderKey))
			if itemKey == "" {
				itemKey = capoverlay.ComputeRenderKey(plan, item)
			}
			if len(itemKey) != 64 || !isLowerHex(itemKey) {
				return OverlayRenderCacheKey{}, false
			}
			parts = append(parts, fmt.Sprintf("%d:%s:%s", index, strings.TrimSpace(item.ID), itemKey))
		}
		renderKey = digest.SHA256String(strings.Join(parts, "\x00"))
	}
	if len(renderKey) != 64 || !isLowerHex(renderKey) {
		return OverlayRenderCacheKey{}, false
	}
	contextHash := overlayPlanRenderContextDigest(plan)
	if contextHash == "" {
		return OverlayRenderCacheKey{}, false
	}
	key := OverlayRenderCacheKey{
		RenderKey:   renderKey,
		PlanContext: contextHash,
		Epoch:       strings.TrimSpace(epoch),
	}
	if key.Epoch == "" {
		key.Epoch = OverlayRenderCacheEpoch()
	}
	if err := key.Validate(); err != nil {
		return OverlayRenderCacheKey{}, false
	}
	return key, true
}

// overlayPlanRenderContextDigest hashes the plan-level facts that change the
// pixels but are NOT part of an item's RenderKey: background/source, animation
// policies, canvas floor, foreground scale, entity variation seed, media
// contract, and the two item fields RenderKey does not carry (Kind and
// CameraMotionID). It deliberately excludes plan identity, Drive routing and
// producer-only fields, so two runs that render the same clip agree.
//
// RenderKey itself is left untouched: persisted plans carry their RenderKey in
// durable checkpoints and Validate re-computes it, so extending that hash would
// invalidate every stored plan. The context digest is where new render inputs
// are added safely.
func overlayPlanRenderContextDigest(plan capoverlay.OverlayPlan) string {
	renderer := strings.TrimSpace(plan.RendererVersion)
	if renderer == "" {
		renderer = "chronon"
	}
	context := struct {
		SchemaVersion   string                    `json:"schema_version"`
		Renderer        string                    `json:"renderer"`
		Width           int                       `json:"width"`
		Height          int                       `json:"height"`
		FPSNum          int                       `json:"fps_num"`
		FPSDen          int                       `json:"fps_den"`
		DurationMS      int64                     `json:"duration_ms"`
		ForegroundScale int                       `json:"foreground_scale_percent"`
		EntitySeed      string                    `json:"entity_variation_seed"`
		MediaContract   string                    `json:"media_contract"`
		Policies        string                    `json:"animation_policies"`
		SourceSHA256    string                    `json:"source_sha256"`
		Background      *overlayCacheBG           `json:"background"`
		ItemKind        string                    `json:"item_kind"`
		CameraMotionID  string                    `json:"camera_motion_id"`
		Items           []overlayCacheItemContext `json:"items,omitempty"`
		// MapBackend is the resolved dynamic-map backend. It is stamped onto
		// the plan AFTER the item's RenderKey was computed, so it must be
		// hashed here or a python-backend clip could satisfy a native-backend
		// request (different renderer, different pixels).
		MapBackend string `json:"map_backend,omitempty"`
		// SamplingIdentity carries the worker's entity-style sampling seed
		// (plan id + video id + item id) for items that opt into entity style
		// selection. Those items are NOT content-addressed across plans: the
		// worker samples a style per plan identity even when the item bytes are
		// identical, so reusing another plan's artifact would silently change
		// the selected style. For those items reuse is limited to retries of
		// the same plan, which is still the expensive case.
		SamplingIdentity string `json:"sampling_identity,omitempty"`
	}{
		SchemaVersion:   strings.TrimSpace(plan.SchemaVersion),
		Renderer:        renderer,
		Width:           plan.Width,
		Height:          plan.Height,
		FPSNum:          plan.FPSNum,
		FPSDen:          plan.FPSDen,
		DurationMS:      plan.DurationMS,
		ForegroundScale: plan.ForegroundScalePercent,
		EntitySeed:      strings.TrimSpace(plan.EntityVariationSeed),
		MediaContract:   strings.TrimSpace(plan.MediaContract),
		Policies:        canonicalJSONString(plan.AnimationPolicies),
		Background:      overlayCacheBackground(plan.Background),
	}
	if plan.Source != nil {
		context.SourceSHA256 = strings.ToLower(strings.TrimSpace(plan.Source.SHA256))
	}
	if len(plan.Items) == 1 {
		item := plan.Items[0]
		context.ItemKind = strings.TrimSpace(item.Kind)
		context.CameraMotionID = strings.TrimSpace(item.CameraMotionID)
		if backend, ok := item.Params["map_backend"].(string); ok {
			context.MapBackend = strings.TrimSpace(backend)
		}
		if strings.TrimSpace(item.EntityStyleID) != "" {
			context.SamplingIdentity = strings.Join([]string{
				strings.TrimSpace(plan.PlanID), strings.TrimSpace(plan.VideoID), strings.TrimSpace(item.ID),
			}, "\x00")
		}
	} else if len(plan.Items) > 1 {
		context.Items = make([]overlayCacheItemContext, 0, len(plan.Items))
		for _, item := range plan.Items {
			fact := overlayCacheItemContext{ID: strings.TrimSpace(item.ID), Kind: strings.TrimSpace(item.Kind), CameraMotionID: strings.TrimSpace(item.CameraMotionID)}
			if backend, ok := item.Params["map_backend"].(string); ok {
				fact.MapBackend = strings.TrimSpace(backend)
			}
			if strings.TrimSpace(item.EntityStyleID) != "" {
				fact.SamplingIdentity = strings.Join([]string{
					strings.TrimSpace(plan.PlanID), strings.TrimSpace(plan.VideoID), strings.TrimSpace(item.ID),
				}, "\x00")
			}
			context.Items = append(context.Items, fact)
		}
	}
	raw, err := json.Marshal(context)
	if err != nil {
		return ""
	}
	return digest.SHA256Bytes(raw)
}

type overlayCacheItemContext struct {
	ID               string `json:"id"`
	Kind             string `json:"kind,omitempty"`
	CameraMotionID   string `json:"camera_motion_id,omitempty"`
	MapBackend       string `json:"map_backend,omitempty"`
	SamplingIdentity string `json:"sampling_identity,omitempty"`
}

// overlayCacheBG is the render-relevant projection of a background. Asset
// references are reduced to their sorted content addresses; producer-only
// paths and URLs are excluded because the worker resolves by address.
type overlayCacheBG struct {
	Kind    string    `json:"kind"`
	Color   []float64 `json:"color,omitempty"`
	Fit     string    `json:"fit,omitempty"`
	Opacity *float64  `json:"opacity,omitempty"`
	Loop    bool      `json:"loop"`
	Style   string    `json:"style,omitempty"`
	Assets  []string  `json:"assets,omitempty"`
}

func overlayCacheBackground(bg *capoverlay.OverlayBackground) *overlayCacheBG {
	if bg == nil {
		return nil
	}
	out := &overlayCacheBG{
		Kind:    strings.TrimSpace(bg.Kind),
		Color:   append([]float64(nil), bg.Color...),
		Fit:     strings.TrimSpace(bg.Fit),
		Opacity: bg.Opacity,
		Loop:    bg.Loop,
		Style:   canonicalJSONString(bg.Style),
	}
	for _, ref := range bg.AssetRefs {
		sha := strings.ToLower(strings.TrimSpace(ref.SHA256))
		if sha == "" {
			continue
		}
		out.Assets = append(out.Assets, sha)
	}
	sort.Strings(out.Assets)
	return out
}

// canonicalJSONString marshals a value deterministically (encoding/json sorts
// map keys) and returns "" for nil/empty values so a `{}` and an absent map
// hash identically.
func canonicalJSONString(value any) string {
	if value == nil {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	if string(raw) == "{}" || string(raw) == "null" || string(raw) == "[]" {
		return ""
	}
	return string(raw)
}

// validCachedOverlayArtifact is the gate every cache hit crosses before it can
// be returned as a completed render. A locator without a content address, a
// non-http URL or a non-positive size is not certifiable and is treated as a
// miss (and invalidated by the caller).
func validCachedOverlayArtifact(artifact *RenderArtifact) bool {
	if artifact == nil {
		return false
	}
	sha := strings.ToLower(strings.TrimSpace(artifact.SHA256))
	if len(sha) != 64 || !isLowerHex(sha) {
		return false
	}
	if artifact.SizeBytes <= 0 {
		return false
	}
	url := strings.TrimSpace(artifact.URL)
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// renderCacheEpochValue resolves the epoch this enqueuer stamps on its keys.
func (e *QueueRenderEnqueuer) renderCacheEpochValue() string {
	if e != nil && strings.TrimSpace(e.renderCacheEpoch) != "" {
		return strings.TrimSpace(e.renderCacheEpoch)
	}
	return OverlayRenderCacheEpoch()
}

type overlayRenderCacheTiming struct {
	lookup  time.Duration
	probe   time.Duration
	store   time.Duration
	outcome string
}

func applyOverlayRenderCacheTiming(wait *RenderCompletionMetrics, timing overlayRenderCacheTiming) {
	if wait == nil {
		return
	}
	wait.CacheLookup = timing.lookup
	wait.CacheProbe = timing.probe
	wait.CacheStore = timing.store
	wait.CacheOutcome = timing.outcome
}

func (e *QueueRenderEnqueuer) lookupOverlayRenderCacheTimed(ctx context.Context, key OverlayRenderCacheKey) (*RenderArtifact, bool, overlayRenderCacheTiming) {
	timing := overlayRenderCacheTiming{outcome: "miss"}
	if e == nil || e.renderCache == nil || !OverlayRenderCacheEnabled() {
		timing.outcome = "disabled"
		return nil, false, timing
	}
	lookupStarted := time.Now()
	artifact, hit, err := e.renderCache.Lookup(ctx, key)
	timing.lookup = time.Since(lookupStarted)
	if err != nil {
		timing.outcome = "lookup_error"
		observability.OverlayRenderCacheTotal.WithLabelValues("lookup_error").Inc()
		return nil, false, timing
	}
	if !hit {
		timing.outcome = "miss"
		observability.OverlayRenderCacheTotal.WithLabelValues("miss").Inc()
		return nil, false, timing
	}
	if !validCachedOverlayArtifact(artifact) {
		timing.outcome = "invalid"
		observability.OverlayRenderCacheTotal.WithLabelValues("invalid").Inc()
		e.invalidateOverlayRenderCache(ctx, key, "cached locator failed certification")
		return nil, false, timing
	}
	if e.renderCacheProber != nil {
		probeStarted := time.Now()
		present, probeErr := e.renderCacheProber.Present(ctx, *artifact)
		timing.probe = time.Since(probeStarted)
		if probeErr != nil {
			timing.outcome = "probe_error"
			// A probing outage must not turn a valid render into a failure:
			// fall through to a normal render, which will overwrite the row.
			observability.OverlayRenderCacheTotal.WithLabelValues("probe_error").Inc()
			return nil, false, timing
		}
		if !present {
			timing.outcome = "invalid"
			observability.OverlayRenderCacheTotal.WithLabelValues("invalid").Inc()
			e.invalidateOverlayRenderCache(ctx, key, "cached artifact is no longer in the object store")
			return nil, false, timing
		}
	}
	artifact = sanitizeCachedOverlayArtifact(artifact)
	timing.outcome = "hit"
	observability.OverlayRenderCacheTotal.WithLabelValues("hit").Inc()
	return artifact, true, timing
}

func (e *QueueRenderEnqueuer) storeOverlayRenderCacheTimed(ctx context.Context, key OverlayRenderCacheKey, artifact *RenderArtifact) (time.Duration, string) {
	started := time.Now()
	if e == nil || e.renderCache == nil || !OverlayRenderCacheEnabled() || !validCachedOverlayArtifact(artifact) {
		return time.Since(started), "skipped"
	}
	if err := e.renderCache.Store(ctx, key, *artifact); err != nil {
		observability.OverlayRenderCacheTotal.WithLabelValues("store_error").Inc()
		return time.Since(started), "store_error"
	}
	observability.OverlayRenderCacheTotal.WithLabelValues("store").Inc()
	return time.Since(started), "stored"
}

func (e *QueueRenderEnqueuer) invalidateOverlayRenderCache(ctx context.Context, key OverlayRenderCacheKey, reason string) {
	if e == nil || e.renderCache == nil {
		return
	}
	if err := e.renderCache.Invalidate(ctx, key, reason); err != nil {
		observability.OverlayRenderCacheTotal.WithLabelValues("invalidate_error").Inc()
		return
	}
	observability.OverlayRenderCacheTotal.WithLabelValues("invalidate").Inc()
}

// sanitizeCachedOverlayArtifact strips worker phase timings and the producing
// job identity from a reused artifact. Those measurements belong to the run
// that produced the bytes; recording them again would attribute another run's
// render service to this one.
func sanitizeCachedOverlayArtifact(artifact *RenderArtifact) *RenderArtifact {
	if artifact == nil {
		return nil
	}
	clean := *artifact
	clean.ID = ""
	clean.MaterializeMS = 0
	clean.PlanMS = 0
	clean.RenderMS = 0
	clean.EncodeMS = 0
	clean.ProbeMS = 0
	clean.HashMS = 0
	clean.UploadMS = 0
	clean.DrivePublishMS = 0
	return &clean
}
