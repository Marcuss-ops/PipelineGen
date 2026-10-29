package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

const mapRunStyleSlot = "run"

func NewDynamicMapGPUAdmission() (DynamicMapGPUAdmission, error) {
	gate, _, _, err := infraoverlays.NewConfiguredGPUGate()
	return gate, err
}

func (e *QueueRenderEnqueuer) renderDynamicMapVideo(ctx context.Context, plan capoverlay.OverlayPlan) (string, error) {
	if e == nil {
		return "", fmt.Errorf("dynamic map renderer enqueuer is nil")
	}
	return renderDynamicMapVideoWithGate(ctx, plan, e.mapGPUGate, e.mapVideoCacheDir)
}

// dynamicMapSamplerForProfile names the map sampler for one render. The
// single map lane is the native renderer of ChrononPlugin, whose certified
// sampling path is CPU-only: the OpenCL sampler is demolition debt together
// with the NVENC encoder family, so there is no sampler knob to override —
// the declared profile and the sampler travel as one contract and both fail
// closed at the boundary when they claim GPU work the renderer cannot do.
func dynamicMapSamplerForProfile(profile scriptoverlay.MapEncodeProfile) string {
	return "cpu"
}

func mapRenderRequiresGPU(profile scriptoverlay.MapEncodeProfile, sampler, composition string) bool {
	return profile.ExpectGPU || sampler != "cpu" || strings.HasPrefix(composition, "ed_")
}

type dynamicMapVideoIdentityManifest struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

func dynamicMapVideoIdentity(path string) (string, int64, error) {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "map.video.identity.json"))
	if err != nil {
		return "", 0, fmt.Errorf("read preverified dynamic map identity: %w", err)
	}
	var identity dynamicMapVideoIdentityManifest
	if err := json.Unmarshal(raw, &identity); err != nil {
		return "", 0, fmt.Errorf("decode preverified dynamic map identity: %w", err)
	}
	if len(identity.SHA256) != 64 || identity.SizeBytes <= 0 || strings.TrimSpace(identity.SHA256) != strings.ToLower(identity.SHA256) {
		return "", 0, fmt.Errorf("preverified dynamic map identity is incomplete or non-canonical")
	}
	for _, ch := range identity.SHA256 {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return "", 0, fmt.Errorf("preverified dynamic map identity has an invalid SHA-256 digest")
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, fmt.Errorf("stat preverified dynamic map video: %w", err)
	}
	if info.Size() != identity.SizeBytes {
		return "", 0, fmt.Errorf("preverified dynamic map video size changed: got=%d want=%d", info.Size(), identity.SizeBytes)
	}
	return identity.SHA256, identity.SizeBytes, nil
}

// projectMapRendererPhases projects the dynamic-map renderer's OWN measured
// phases from the chronon.dynamic-map-telemetry.v1 summary onto the run bound
// to ctx as canonical operations under StageOverlayRender (prefetch ← tile
// prefetch, prepare ← plate prepare, encode ← render+encode wall). The
// renderer measured these phases and reported them in its summary; the kernel
// records the owner-reported durations and never re-times the boundary. Until
// this projection existed the map item cost (42–90 s/item on the golden case)
// landed entirely in the unmeasured residual of the overlay stage. A phase
// the summary did not measure (non-positive) is skipped — never a fake zero.
// Units are normalized to milliseconds: the summary carries prefetch and
// prepare in ms and the encode wall in seconds.
func projectMapRendererPhases(ctx context.Context, tilePrefetchMS, platePrepareMS, renderEncodeWallS float64) {
	record := func(op kernobs.OperationName, ms float64) {
		if ms <= 0 {
			return
		}
		kernobs.RecordOperation(ctx, kernobs.OperationInfo{
			Stage:     StageOverlayRender,
			Component: kernobs.ComponentChronon,
			Operation: op,
		}, int64(ms+0.5))
	}
	record(kernobs.OperationPrefetch, tilePrefetchMS)
	record(kernobs.OperationPrepare, platePrepareMS)
	record(kernobs.OperationEncode, renderEncodeWallS*1000)
}

// PreMaterializeDynamicMapVideo renders the dynamic map video of EVERY map
// item in the plan into the durable cache WITHOUT submitting anything to the
// render queue (TICKET-OVERLAY-THROUGHPUT: «le mappe vanno pre-materializzate
// fuori dal percorso di sottomissione (stessa direzione di P3, ma sul
// prepare)»). The submission path (enqueueChrononPlan's background fold)
// consumes the cache entry, so its boundary stays thin and the map prepare is
// attributable to the prepare phase. Concurrent callers for the same map
// share one render (joinDynamicMapVideoRender): an item whose prepare is
// still running joins it instead of launching a second Chronon map render.
//
// Identity rule: a single-map plan is prepared under the exact plan identity
// its fold resolves; a multi-map plan (or a map beside other items) is
// prepared per map under the exact single-item child identity the per-item
// route folds (separateOverlayItemPlan), so every prepared cache key is a key
// the submission consumes — one Chronon map render per map whatever the
// plan's shape. The render workspace is released here once the durable cache
// entry is stored: the cache entry is the only deliverable (each fold
// materializes its own private workspace from the cache).
func (e *QueueRenderEnqueuer) PreMaterializeDynamicMapVideo(ctx context.Context, plan capoverlay.OverlayPlan) error {
	if e == nil {
		return fmt.Errorf("queue render enqueuer is not configured")
	}
	// The SAME normalization the submission's fold applies, so both sides
	// resolve the same durable cache key.
	semanticPlan, decisions, err := normalizeDynamicMapFoldPlan(plan)
	if err != nil {
		return err
	}
	if len(decisions) == 0 {
		return nil
	}
	if len(semanticPlan.Items) == 1 && len(decisions) == 1 {
		return e.preMaterializeMapUnit(ctx, semanticPlan, decisions)
	}
	for index, item := range plan.Items {
		if item.Map == nil {
			continue
		}
		child, _, err := separateOverlayItemPlan(plan, item, index)
		if err != nil {
			return fmt.Errorf("prepare overlay map item %q: %w", item.ID, err)
		}
		childPlan, childDecisions, err := normalizeDynamicMapFoldPlan(child)
		if err != nil {
			return fmt.Errorf("prepare overlay map item %q: %w", item.ID, err)
		}
		if err := e.preMaterializeMapUnit(ctx, childPlan, childDecisions); err != nil {
			return fmt.Errorf("prepare overlay map item %q: %w", item.ID, err)
		}
	}
	return nil
}

// preMaterializeMapUnit prepares ONE single-map unit (already normalized)
// into the durable cache and releases its render workspace. A native-backend
// map renders on the worker and has no cache entry to prepare.
func (e *QueueRenderEnqueuer) preMaterializeMapUnit(ctx context.Context, semanticPlan capoverlay.OverlayPlan, decisions []planMapBackend) error {
	pythonMaps := 0
	for _, decision := range decisions {
		if decision.backend == scriptoverlay.MapBackendDynamic {
			pythonMaps++
		}
	}
	if pythonMaps != 1 {
		return nil
	}
	path, err := e.renderDynamicMapVideo(ctx, semanticPlan)
	if err != nil {
		return err
	}
	// Workspace ownership: this prepare owns the workspace it received and
	// releases it after the durable cache entry is stored (the store completes
	// before the render returns). The fold later materializes its own private
	// workspace from the cache, so nothing reads this one again.
	_ = os.RemoveAll(filepath.Dir(path))
	return nil
}

// renderDynamicMapVideoWithGate de-duplicates concurrent renders of the same
// map video around renderDynamicMapVideoOnce (the render body).
func renderDynamicMapVideoWithGate(ctx context.Context, plan capoverlay.OverlayPlan, gate DynamicMapGPUAdmission, cacheDirs ...string) (string, error) {
	return joinDynamicMapVideoRender(ctx, plan, func() (string, error) {
		return renderDynamicMapVideoOnce(ctx, plan, gate, cacheDirs...)
	})
}

// resolveMapBasemap maps the runtime map_type through the shared map registry
// and the single native renderer's certified basemap subset
// (scriptoverlay.NativeMapBasemaps). The retired Python renderer fetched Esri
// raster tiles; the native renderer draws the editorial vector basemap only,
// so a raster style fails closed by name (never a silent style swap) and
// "random" draws from the servable subset. The registry vocabulary stays
// cross-repo canonical; servability is decided here, at execution.
func resolveMapBasemap(mapType, seed, slot string) (string, error) {
	servable := scriptoverlay.NativeMapBasemaps()
	// Empty means "no map_type requested": the registry's default is an Esri
	// raster style (cross-repo canonical, unchanged), which the native renderer
	// does not draw — so the empty request takes the servable draw, exactly
	// like "random". applyMapPresentationToItems always writes the concrete
	// default; this branch covers hand-built plans.
	if strings.TrimSpace(mapType) == "" || strings.EqualFold(strings.TrimSpace(mapType), "random") {
		pool := make([]string, 0, len(servable))
		for _, style := range mapBasemapStyles() {
			if servable[style] {
				pool = append(pool, style)
			}
		}
		selected := deterministicMapStyle(seed, "map_basemap", slot, pool)
		if selected == "" {
			return "", fmt.Errorf("no certified map basemap available")
		}
		return selected, nil
	}
	id, ok := capoverlay.AnimationMapTypeBasemap(mapType)
	if !ok {
		return "", fmt.Errorf("unsupported runtime map_type %q", mapType)
	}
	if !servable[id] {
		return "", fmt.Errorf("basemap %q (map_type %q) is not ported to the native map renderer (raster tile family, demolition debt, TICKET-MAPS-NATIVE-PORT): declare map_type chronon_editorial", id, mapType)
	}
	return id, nil
}

// resolveMapAnimation resolves ONE runtime map animation. An explicit
// certified id wins verbatim (one choice per runtime, applied to every map
// item by applyMapPresentationToItems). Empty/random samples the restrained
// random pool deterministically so replays stay byte-identical. Certified
// holds every id the Python renderer accepts; randomPool is the subset the
// sampler may pick when the caller chose nothing.
func resolveMapAnimation(requested, seed, slot, channel string, certified, randomPool []string) (string, error) {
	requested = strings.TrimSpace(requested)
	if channel == "map_label" && len(certified) == 0 {
		if requested == "" || strings.EqualFold(requested, "random") || requested == "none" {
			return "none", nil
		}
		return "", fmt.Errorf("unsupported runtime %s %q", channel, requested)
	}
	if requested == "" || strings.EqualFold(requested, "random") {
		if len(randomPool) == 0 {
			randomPool = certified
		}
		selected := deterministicMapStyle(seed, channel, slot, randomPool)
		if selected == "" {
			return "", fmt.Errorf("no certified map animation available for %s", channel)
		}
		return selected, nil
	}
	for _, candidate := range certified {
		if requested == candidate {
			return requested, nil
		}
	}
	return "", fmt.Errorf("unsupported runtime %s %q", channel, requested)
}

func dynamicMapTypography(params map[string]any) map[string]any {
	// Canonical job visual config wins over map-local defaults: family, fill
	// and editorial style come from the job so maps match phrases/captions.
	// Map-local geometry (size, placement, glow) may still differ; only the
	// typeface identity and palette are unified. Overrides via item params or
	// job_style are explicit; hardcoded values are the last-resort fallback.
	baseFamily, baseFill := "bricolage_grotesque", "#F8F5EA"
	if job, ok := params["job_style"].(map[string]any); ok {
		if v, ok := job["font_family"].(string); ok && v != "" {
			baseFamily = v
		}
		if v, ok := job["fill"].(string); ok && v != "" {
			baseFill = v
		}
		if v, ok := job["font_color"].(string); ok && v != "" {
			baseFill = v
		}
	}
	if v, ok := params["job_font_family"].(string); ok && v != "" {
		baseFamily = v
	}
	if v, ok := params["job_fill"].(string); ok && v != "" {
		baseFill = v
	}
	style := map[string]any{
		"font_family": baseFamily, "font_size_px": 92.0,
		"fill": baseFill, "stroke_size": 2.0,
		"shadow_color": "#000000", "shadow_opacity": 0.7,
		"shadow_blur_px": 14.0, "shadow_offset_x_px": 0.0,
		"shadow_offset_y_px": 4.0, "glow_size": 10.0,
		"glow_intensity": 0.4, "glow_color": "#FFFFFF",
	}
	for _, key := range []string{"font_family", "font_size_px", "stroke_size", "shadow_opacity",
		"shadow_blur_px", "shadow_offset_x_px", "shadow_offset_y_px", "glow_size"} {
		if value, ok := params[key]; ok {
			style[key] = value
		}
	}
	if nested, ok := params["style"].(map[string]any); ok {
		if fill, ok := nested["fill"].(string); ok && strings.TrimSpace(fill) != "" {
			style["fill"] = fill
		}
		if shadow, ok := nested["shadow"].(map[string]any); ok {
			if color, ok := shadow["color"].(string); ok && color != "" {
				style["shadow_color"] = color
			}
		}
	}
	return style
}

// boundedMapRenderOutput caps retained child-process logs while keeping the
// most recent diagnostics. The machine-readable summary is written separately.
type boundedMapRenderOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedMapRenderOutput) Write(p []byte) (int, error) {
	const maxBytes = 64 * 1024
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > maxBytes {
		b.data = append([]byte(nil), b.data[len(b.data)-maxBytes:]...)
	}
	return len(p), nil
}

func (b *boundedMapRenderOutput) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

// mapBasemapStyles returns the active cartographic palette for map_type=random.
// TestMapBasemapStylesMatchChrononPalette guards cross-repository drift.
func mapBasemapStyles() []string {
	return capoverlay.AnimationCandidates("map.basemap")
}

var (
	mapCameraAnimations = capoverlay.AnimationRandomCandidates("map.camera")
	mapLabelAnimations  = capoverlay.AnimationRandomCandidates("map.label")
)

// mapCameraAnimationsCertified lists every camera id the Python renderer
// accepts, including explicit-only choices. resolveMapAnimation validates
// against this list; the random sampler draws from mapCameraAnimations.
func mapCameraAnimationsCertified() []string {
	return capoverlay.AnimationCandidates("map.camera")
}
