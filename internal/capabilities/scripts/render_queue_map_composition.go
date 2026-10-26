package scriptgeneration

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// The composition-selection facade moved here from render_queue_map.go
// (strict 600-LOC per-file cap): the root package keeps the renderer,
// this file keeps the overlay-subpackage compatibility wrappers.

// The composition selection lives in the overlay subpackage (the sanctioned
// migration target). These thin wrappers keep the root package as the
// compatibility facade for existing callers and tests.

type mapCompositionInput = scriptoverlay.MapCompositionInput

const (
	MapFamilyLocationFocus = scriptoverlay.MapFamilyLocationFocus
	MapFamilyMultiPin      = scriptoverlay.MapFamilyMultiPin
	MapFamilyJourney       = scriptoverlay.MapFamilyJourney
	MapFamilyRegion        = scriptoverlay.MapFamilyRegion
	MapFamilyComparison    = scriptoverlay.MapFamilyComparison
	MapFamilyEditorial     = scriptoverlay.MapFamilyEditorial
)

func mapCompositionFamily(id string) scriptoverlay.MapCompositionFamily {
	return scriptoverlay.MapCompositionFamilyOf(id)
}

func eligibleMapCompositionsFor(input mapCompositionInput, certified []string) []string {
	return scriptoverlay.EligibleMapCompositionsFor(input, certified)
}

func resolveMapCompositionFor(requested, seed, slot string, input mapCompositionInput) (string, string, error) {
	return scriptoverlay.ResolveMapCompositionFor(requested, seed, slot, input)
}

// resolveMapComposition is the classic selection API retained for callers and
// tests that hold only the pin count.
func resolveMapComposition(requested, seed, slot string, pinCounts ...int) (string, error) {
	input := mapCompositionInput{}
	if len(pinCounts) > 0 {
		input.PinCount = pinCounts[0]
	}
	selected, _, err := resolveMapCompositionFor(requested, seed, slot, input)
	return selected, err
}

// eligibleMapCompositions removes only renderer-documented multi-pin systems
// that cannot run for this map's grounded pin count. Explicit selections still
// validate against the full certified catalog and fail closed in the renderer.
func eligibleMapCompositions(pinCount int, certified []string) []string {
	return scriptoverlay.EligibleMapCompositions(pinCount, certified)
}

// mapMapAnimationsCertified lists every renderer-approved map_animation id:
// map systems from map_compositions plus the GPU-certified editorial
// scenes in map_editorial.MAP_EDITORIAL_SCENES.
// TestMapCompositionsMatchPython guards cross-repository drift.
func mapMapAnimationsCertified() []string {
	return capoverlay.AnimationCandidates("map.composition")
}

// mapLabelAnimationsCertified lists every label id the Python renderer
// accepts, including explicit-only choices.
func mapLabelAnimationsCertified() []string {
	return capoverlay.AnimationCandidates("map.label")
}

// mapRunSeed is the stable per-run seed for a map's presentation choices. The
// plan fingerprint is the canonical variant knob — a new fingerprint IS a new
// variant — with PlanID/VideoID as fallbacks for a plan stamped before the
// fingerprint was computed. It never reads wall-clock or process state, so the
// same run always resolves the same map presentation.
func mapRunSeed(plan capoverlay.OverlayPlan) string {
	// Child render plans get their own fingerprint and ID. Prefer the parent
	// job identity so a run-level style choice remains the same across all map
	// clips belonging to that video.
	for _, candidate := range []string{plan.ResultJobID, plan.DriveJobID, plan.Fingerprint, plan.PlanID, plan.VideoID} {
		if seed := strings.TrimSpace(candidate); seed != "" {
			return seed
		}
	}
	return "map"
}

// deterministicMapStyle resolves ONE presentation value from options as a pure
// function of the run seed and the style channel. It samples through the
// overlays package's single deterministic sampler, so map presentation obeys
// exactly the same seed→choice contract as every other semantic selection in
// the pipeline. The channel keeps the camera move and basemap independent:
// the same seed picks them from separate digests.
func deterministicMapStyle(seed, channel, itemID string, options []string) string {
	return capoverlay.DefaultDeterministicPresetSampler.Sample(capoverlay.PresetSampleInput{
		JobFingerprint: seed,
		SceneID:        "map",
		SemanticID:     itemID,
		PresetFamily:   channel,
		Presets:        options,
	}).Preset
}

// ── merged from dynamic_map_video_cache.go ─────────────────────────────────────

const (
	dynamicMapVideoCacheDirEnv   = "VELOX_MAP_VIDEO_CACHE_DIR"
	dynamicMapVideoCacheEpochEnv = "VELOX_MAP_VIDEO_CACHE_EPOCH"
	dynamicMapVideoCacheMaxEnv   = "VELOX_MAP_VIDEO_CACHE_MAX_BYTES"
	dynamicMapVideoCacheMaxBytes = int64(10 << 30)
)

type dynamicMapVideoCacheManifest struct {
	Schema    string          `json:"schema"`
	Key       string          `json:"key"`
	SHA256    string          `json:"sha256"`
	SizeBytes int64           `json:"size_bytes"`
	Telemetry json.RawMessage `json:"telemetry"`
}

// dynamicMapVideoCacheKey addresses the actual resolved render request rather
// than map tiles: payload contains resolved camera/style/timing/pins, while
// scriptDigest, encoder profile, sampler and epoch fence renderer changes.
// ── dynamic-map video render de-duplication (TICKET-OVERLAY-THROUGHPUT) ──────
//
// «le mappe vanno pre-materializzate fuori dal percorso di sottomissione»: the
// map video is rendered by the PREPARE phase and the submission path consumes
// the durable cache entry. Both paths call renderDynamicMapVideoWithGate, so
// concurrent callers for the same map must share one render: a second Chronon
// map render for one key wastes tens of seconds of CPU/GPU and doubles the
// shared GPU-gate hold.

type dynamicMapVideoPromise struct {
	done chan struct{}
	err  error
}

var dynamicMapVideoRenders sync.Map // flight key -> *dynamicMapVideoPromise

// ── map backend routing (moved from render_queue.go) ────────────────

// planMapBackend is one map item's render-boundary backend decision: the
// capability-resolved backend plus the reason recorded for the map decision
// report. resolvePlanMapBackends is the SINGLE owner of this decision — the
// per-item routing predicate and the queue annotation below both consume its
// output, so the route a map takes and the backend stamped on it can never
// disagree (the P0 rule: one decision, made per map from its own
// requirements, never from the plan's item count).
type planMapBackend struct {
	itemID  string
	backend scriptoverlay.MapBackend
	reason  string
}

// resolvePlanMapBackends resolves every map item's backend from its own real
// render requirements. It fails closed on the first unresolvable item.
// normalizeDynamicMapFoldPlan applies the fold's plan normalization — the
// stable VideoID fallback and the per-map backend annotation — and returns the
// plan the dynamic-map render consumes together with its backend decisions.
// The prepare phase (PreMaterializeDynamicMapVideo) and the submission's
// background fold MUST render through this same normalization, or their cache
// keys differ and one of them pays a second Chronon map render.
func normalizeDynamicMapFoldPlan(plan capoverlay.OverlayPlan) (capoverlay.OverlayPlan, []planMapBackend, error) {
	semanticPlan := plan
	if strings.TrimSpace(semanticPlan.VideoID) == "" {
		semanticPlan.VideoID = semanticPlan.PlanID
	}
	decisions, err := resolvePlanMapBackends(semanticPlan)
	if err != nil {
		return capoverlay.OverlayPlan{}, nil, err
	}
	annotatePlanMapBackends(&semanticPlan, decisions)
	return semanticPlan, decisions, nil
}

func resolvePlanMapBackends(plan capoverlay.OverlayPlan) ([]planMapBackend, error) {
	nativeDiagnostic := os.Getenv("PIPELINEGEN_MAP_NATIVE_RENDER") == "1"
	var decisions []planMapBackend
	for i := range plan.Items {
		if plan.Items[i].Map == nil {
			continue
		}
		backend, reason, err := scriptoverlay.ResolveMapItemBackend(plan.Items[i], nativeDiagnostic)
		if err != nil {
			return nil, fmt.Errorf("resolve map backend for item %q: %w", plan.Items[i].ID, err)
		}
		decisions = append(decisions, planMapBackend{itemID: plan.Items[i].ID, backend: backend, reason: reason})
	}
	return decisions, nil
}

// annotatePlanMapBackends stamps the single backend decision onto every map
// item, so the worker executes exactly the backend the capability resolution
// chose. It is the second half of the single decision (resolve, then
// annotate) — callers never re-resolve per item.
//
// Overlay items share their Params map after shallow copies (parent plan →
// per-item child plans), and children are prepared CONCURRENTLY. Write the
// annotation into a CLONED params map, never the shared one — the same rule
// as enter_frames in separateOverlayItemPlan: a child-only decision must not
// mutate state other goroutines are fingerprinting and marshaling.
func annotatePlanMapBackends(plan *capoverlay.OverlayPlan, decisions []planMapBackend) {
	if plan == nil {
		return
	}
	byItem := make(map[string]planMapBackend, len(decisions))
	for _, decision := range decisions {
		byItem[decision.itemID] = decision
	}
	for itemIndex := range plan.Items {
		if plan.Items[itemIndex].Map == nil {
			continue
		}
		decision, ok := byItem[plan.Items[itemIndex].ID]
		if !ok {
			continue
		}
		params := make(map[string]any, len(plan.Items[itemIndex].Params)+2)
		for key, value := range plan.Items[itemIndex].Params {
			params[key] = value
		}
		params["map_backend"] = string(decision.backend)
		params["map_backend_reason"] = decision.reason
		plan.Items[itemIndex].Params = params
	}
}

// ── map item lineage (moved from render_queue_items.go) ─────────────

func mapEntityLineage(mapOverlay *capoverlay.MapOverlay) ([]string, []string) {
	if mapOverlay == nil {
		return nil, nil
	}
	ids := make([]string, 0, len(mapOverlay.Pins))
	labels := make([]string, 0, len(mapOverlay.Pins))
	seen := make(map[string]struct{}, len(mapOverlay.Pins))
	for _, pin := range mapOverlay.Pins {
		id, label := strings.TrimSpace(pin.ID), strings.TrimSpace(pin.Label)
		if id != "" {
			if _, exists := seen[id]; !exists {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
		if label != "" {
			labels = append(labels, label)
		}
	}
	return ids, labels
}

// ── dynamic map renderer fingerprint (moved from render_queue_map.go) ──

// dynamicMapRendererFingerprint is the cache identity of the map renderer:
// the SHA-256 of the native renderer binary (ChrononPlugin's
// maps_dynamic_map_render). The binary IS the built renderer code — camera,
// sampler, tile raster, labels — so any rebuild invalidates cached MP4s. The
// renderer additionally bakes its own source fingerprint into the telemetry
// summary (renderer_fingerprint), so an artifact can always be re-attributed
// to the code that produced it.
func dynamicMapRendererFingerprint(nativeRenderer string) (string, error) {
	data, err := os.ReadFile(nativeRenderer)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("native map renderer %s is empty", nativeRenderer)
	}
	return digest.SHA256Bytes(data), nil
}

// ── dynamic map import attestation (merged from render_queue_map_import.go) ──

const dynamicMapImportSchema = "renderinggen.overlay-import.v1"

// dynamicMapImportRequest is the versioned producer attestation consumed by
// RenderingGen's existing overlay.import worker. Its assertions only describe
// the source; RenderingGen independently hashes, probes and fully decodes it.
type dynamicMapImportRequest struct {
	SchemaVersion     string          `json:"schema_version"`
	Producer          string          `json:"producer"`
	PlanID            string          `json:"plan_id"`
	VideoID           string          `json:"video_id"`
	ItemIDs           []string        `json:"item_ids"`
	SourceSHA256      string          `json:"source_sha256"`
	SourceSizeBytes   int64           `json:"source_size_bytes"`
	Width             int             `json:"width"`
	Height            int             `json:"height"`
	FPSNum            int             `json:"fps_num"`
	FPSDen            int             `json:"fps_den"`
	DurationUS        int64           `json:"duration_us"`
	FrameCount        int             `json:"frame_count"`
	ProducerTelemetry json.RawMessage `json:"producer_telemetry,omitempty"`
}

func marshalDynamicMapImport(plan capoverlay.OverlayPlan, sha string, size int64, frameCount int, durationUS int64, telemetry []byte) ([]byte, error) {
	if len(telemetry) == 0 || !json.Valid(telemetry) {
		return nil, fmt.Errorf("dynamic map import requires valid producer telemetry")
	}
	if len(plan.Items) != 1 || plan.Items[0].Map == nil || strings.TrimSpace(plan.Items[0].ID) == "" {
		return nil, fmt.Errorf("dynamic map import requires exactly one identified map item")
	}
	request := dynamicMapImportRequest{
		SchemaVersion: dynamicMapImportSchema, Producer: "pipelinegen-map-v2",
		PlanID: plan.PlanID, VideoID: plan.VideoID,
		ItemIDs:      []string{plan.Items[0].ID},
		SourceSHA256: sha, SourceSizeBytes: size,
		Width: plan.Width, Height: plan.Height,
		FPSNum: plan.FPSNum, FPSDen: plan.FPSDen,
		DurationUS: durationUS, FrameCount: frameCount,
		ProducerTelemetry: append(json.RawMessage(nil), telemetry...),
	}
	if request.PlanID == "" || request.VideoID == "" || len(sha) != 64 || size <= 0 ||
		request.Width <= 0 || request.Height <= 0 || request.FPSNum <= 0 || request.FPSDen <= 0 ||
		durationUS <= 0 || frameCount <= 0 {
		return nil, fmt.Errorf("dynamic map import source identity and media facts must be positive and complete")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("encode dynamic map import request: %w", err)
	}
	return encoded, nil
}

// hashDynamicMapVideo identifies finalized bytes before cache storage copies
// them and before the importer consumes the identity manifest.
func hashDynamicMapVideo(path string) (string, int64, error) {
	return digest.SHA256File(path)
}
