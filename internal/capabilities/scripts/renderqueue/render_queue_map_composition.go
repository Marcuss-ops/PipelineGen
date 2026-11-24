package renderqueue

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// The composition-selection facade moved here from render_queue_map.go
// (strict 600-LOC per-file cap): the root package keeps the renderer,
// this file keeps the overlay-subpackage compatibility wrappers.

// The composition selection lives in the overlay subpackage (the sanctioned
// migration target). These thin wrappers keep the root package as the
// compatibility facade for existing callers and tests.

type mapCompositionInput = capoverlay.MapCompositionInput

const (
	MapFamilyLocationFocus = capoverlay.MapFamilyLocationFocus
	MapFamilyMultiPin      = capoverlay.MapFamilyMultiPin
	MapFamilyJourney       = capoverlay.MapFamilyJourney
	MapFamilyRegion        = capoverlay.MapFamilyRegion
	MapFamilyComparison    = capoverlay.MapFamilyComparison
	MapFamilyEditorial     = capoverlay.MapFamilyEditorial
)

func mapCompositionFamily(id string) capoverlay.MapCompositionFamily {
	return capoverlay.MapCompositionFamilyKindOf(id)
}

func resolveMapCompositionFor(requested, seed, slot string, input mapCompositionInput, randomPools ...[]string) (string, string, error) {
	return capoverlay.ResolveMapCompositionFor(requested, seed, slot, input, randomPools...)
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
func mapRunSeed(plan capmodel.OverlayPlan) string {
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
	backend capoverlay.MapBackend
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
func normalizeDynamicMapFoldPlan(plan capmodel.OverlayPlan) (capmodel.OverlayPlan, []planMapBackend, error) {
	semanticPlan := plan
	if strings.TrimSpace(semanticPlan.VideoID) == "" {
		semanticPlan.VideoID = semanticPlan.PlanID
	}
	decisions, err := resolvePlanMapBackends(semanticPlan)
	if err != nil {
		return capmodel.OverlayPlan{}, nil, err
	}
	annotatePlanMapBackends(&semanticPlan, decisions)
	return semanticPlan, decisions, nil
}

func resolvePlanMapBackends(plan capmodel.OverlayPlan) ([]planMapBackend, error) {
	var decisions []planMapBackend
	for i := range plan.Items {
		if plan.Items[i].Map == nil {
			continue
		}
		backend, reason, err := capoverlay.ResolveMapItemBackend(plan.Items[i])
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
func annotatePlanMapBackends(plan *capmodel.OverlayPlan, decisions []planMapBackend) {
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

func mapEntityLineage(mapOverlay *capmodel.MapOverlay) ([]string, []string) {
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
	// Stream the hash: the native renderer is a large binary and reading it
	// whole into RAM just to fingerprint it pinned a copy per call.
	sum, size, err := digest.SHA256File(nativeRenderer)
	if err != nil {
		return "", err
	}
	if size == 0 {
		return "", fmt.Errorf("native map renderer %s is empty", nativeRenderer)
	}
	return sum, nil
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

func marshalDynamicMapImport(plan capmodel.OverlayPlan, sha string, size int64, frameCount int, durationUS int64, telemetry []byte) ([]byte, error) {
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

// ── map render presentation (the item's LOOK, resolved before any render) ──

// mapRenderPresentation is the map item's presentation, resolved once before
// any render work: the wire pins with their spoken schedule, the camera
// framing, the composition/label/basemap choices and the planner's group
// identity. Resolving it in one place keeps the payload, the decision report
// and the renderer admission reading the SAME decision instead of recomputing
// (and possibly disagreeing about) it.
type mapRenderPresentation struct {
	Pins        []map[string]any
	PinTiming   []map[string]any
	AnchorLat   float64
	AnchorLon   float64
	StartZoom   float64
	EndZoom     float64
	SendZooms   bool
	Group       map[string]any
	Strategy    string
	Requested   string
	Camera      string
	Label       string
	Composition string
	Fallback    string
	Preset      string
	Basemap     string
	Territory   string
	Country     string
	Timeline    string
	Mountain    string
	Scope       string
	MapLevel    string
}

// mapRenderWirePins projects the grounded pins onto the native renderer's wire
// contract: place identity only (id, label, latitude, longitude). The renderer
// refuses fields it cannot draw, and per-pin presentation extras belong to
// composed presentations that are not ported — the plain projection below
// never smuggles unported fields onto the wire.
func mapRenderWirePins(item capmodel.OverlayItem) []map[string]any {
	pins := make([]map[string]any, 0, len(item.Map.Pins))
	for _, pin := range item.Map.Pins {
		pins = append(pins, map[string]any{"id": pin.ID, "label": pin.Label, "latitude": pin.Latitude, "longitude": pin.Longitude})
	}
	return pins
}

func stringSliceParam(params map[string]any, key string) []string {
	values, _ := params[key].([]string)
	return append([]string(nil), values...)
}

// mapRenderStyleSlot selects the map treatment per map ITEM, not per parent
// run: a video holding several maps varies compositions and basemaps instead
// of repeating the same treatment on every map clip, while the run seed keeps
// a replay byte-identical.
func mapRenderStyleSlot(plan capmodel.OverlayPlan) string {
	if slot := strings.TrimSpace(plan.Items[0].ID); slot != "" {
		return slot
	}
	return mapRunStyleSlot
}

// resolveMapRenderPresentation resolves the camera, label, composition and
// basemap of one map item. Every choice is deterministic in (seed, item) or
// grounded data; nothing is re-derived at render time.
func resolveMapRenderPresentation(plan capmodel.OverlayPlan, seed, slot string, renderWidth, renderHeight int) (mapRenderPresentation, error) {
	item := plan.Items[0]
	pins := mapRenderWirePins(item)
	// Per-pin spoken timing and the planner's group decision ride the item
	// params (map_pin_timing / map_group / map_group_strategy) and enter the
	// renderer payload so the camera activates each place exactly when the
	// voiceover names it. The renderer never re-derives editorial timing.
	pinTiming := capoverlay.MapPinTimingPayload(item.Params, pins)
	// The single-anchor native camera framing: the anchor is the mean of the
	// drawn places, and the zoom interval is either the planner's grounded
	// camera move or a deterministic fit keeping every place in frame. The
	// certified product dive (5.2→17.8) stays the renderer's own default for
	// a single place with no grounded move — its zoom keys are omitted.
	cameraMove := item.Map.CameraMove
	anchorLat, anchorLon, startZoom, endZoom, sendZooms := capoverlay.MapNativeCameraFraming(
		pins, renderWidth, renderHeight, cameraMove != nil,
		cameraMoveZoom(cameraMove, true), cameraMoveZoom(cameraMove, false))
	group := capoverlay.MapGroupPayload(item.Params)
	strategy := capoverlay.MapGroupStrategyParam(item.Params)
	cameraChoice, _ := item.Params["map_camera_animation"].(string)
	labelChoice, _ := item.Params["map_label_animation"].(string)
	cameraAnimation, err := resolveMapAnimation(cameraChoice, seed, slot, "map_camera", mapCameraAnimationsCertified(), mapCameraAnimations)
	if err != nil {
		return mapRenderPresentation{}, err
	}
	labelAnimation, err := resolveMapAnimation(labelChoice, seed, slot, "map_label", mapLabelAnimationsCertified(), capoverlay.AnimationRandomCandidates("map.label"))
	if err != nil {
		return mapRenderPresentation{}, err
	}
	compositionChoice, _ := item.Params["map_animation"].(string)
	// A preset is the stable alias of one (composition, basemap) pair. The
	// plan already carries the expanded pair, but the render boundary is the
	// last owner: it re-applies the pair and refuses an unknown preset instead
	// of rendering a look the caller did not ask for.
	presetID, _ := item.Params["map_preset"].(string)
	presetID = strings.TrimSpace(presetID)
	var preset capoverlay.MapPreset
	if presetID != "" {
		var known bool
		preset, known = capoverlay.LookupMapPreset(presetID)
		if !known {
			return mapRenderPresentation{}, fmt.Errorf("map_preset %q is not a certified map preset", presetID)
		}
		compositionChoice = preset.Animation
	}
	// Wire DATA resolution BEFORE composition admission (TICKET map parity):
	// the ported territory family carries the ADM3 shapeName of the map's
	// FIRST pin — the same exact spelling the native renderer matches against
	// the SAME catalog file. Empty means the catalog could not resolve the
	// pin: the resolver's data-compatibility gate then keeps territory
	// compositions out of the random draw and gives explicit requests a
	// precise failure, never a guessed name the renderer would refuse deep
	// inside its render loop.
	territoryFeature, territoryScope := mapTerritoryForFirstPin(item)
	countryFeature := ""
	if configured, _ := item.Params["country_feature"].(string); strings.TrimSpace(configured) != "" {
		countryFeature = strings.TrimSpace(configured)
	} else if territoryFeature != "" {
		// The shipped ADM3 source contains Italian municipalities only. A
		// successful point-in-polygon match therefore grounds this country.
		countryFeature = "Italy"
	}
	timelineLabel, _ := item.Params["timeline_label"].(string)
	mountainAsset, _ := item.Params["mountain_overlay_asset"].(string)
	if strings.TrimSpace(mountainAsset) == "" {
		mountainAsset = capoverlay.MapMountainOverlayDefault()
	}
	mapAnimation, mapFallbackReason, err := resolveMapCompositionFor(compositionChoice, seed, slot, mapCompositionInput{
		PinCount:             len(item.Map.Pins),
		Pins:                 pins,
		DurationUS:           capoverlay.MapItemDurationMS(item, plan.DurationMS) * 1000,
		Strategy:             strategy,
		Camera:               cameraAnimation,
		Label:                labelAnimation,
		TerritoryFeature:     territoryFeature,
		TerritoryScope:       territoryScope,
		AreaGlowRadiusKM:     item.Map.AreaGlowRadiusKM,
		CountryFeature:       countryFeature,
		TimelineLabel:        timelineLabel,
		MountainOverlayAsset: mountainAsset,
	}, stringSliceParam(item.Params, "map_composition_pool"))
	if err != nil {
		return mapRenderPresentation{}, err
	}
	if strings.HasPrefix(mapAnimation, "ed_") || strings.HasPrefix(mapAnimation, "editorial_") {
		// Editorial scene packs own their labels and marker.
		labelAnimation = "none"
		if strings.HasPrefix(mapAnimation, "editorial_") {
			cameraAnimation = "editorial_pack_push_in"
			sendZooms = false
		}
	}
	mapType, _ := item.Params["map_type"].(string)
	stylePool := stringSliceParam(item.Params, "map_style_pool")
	if presetID != "" {
		mapType = preset.Basemap
		stylePool = []string{preset.Basemap}
	}
	var basemapStyle string
	if strings.HasPrefix(mapAnimation, "editorial_") {
		if strings.TrimSpace(mapType) != "" && !strings.EqualFold(strings.TrimSpace(mapType), "random") {
			basemapStyle, err = resolveMapBasemap(mapType, seed, slot, stylePool)
			if err == nil && basemapStyle != "chronon_editorial" {
				err = fmt.Errorf("map animation %q requires style=chronon_editorial", mapAnimation)
			}
		} else if containsMapStyle(stylePool, "chronon_editorial") {
			basemapStyle = "chronon_editorial"
		} else {
			err = fmt.Errorf("map animation %q requires chronon_editorial in style_pool", mapAnimation)
		}
	} else {
		basemapStyle, err = resolveMapBasemap(mapType, seed, slot, stylePool)
	}
	if err != nil {
		return mapRenderPresentation{}, err
	}
	if cameraAnimation == "" || labelAnimation == "" || basemapStyle == "" {
		return mapRenderPresentation{}, fmt.Errorf("dynamic map presentation catalog is empty")
	}
	mapLevel, _ := item.Params["map_level"].(string)
	mapLevel = strings.ToLower(strings.TrimSpace(mapLevel))
	if mapLevel == "auto" {
		mapLevel = ""
	}
	return mapRenderPresentation{
		Pins: pins, PinTiming: pinTiming,
		AnchorLat: anchorLat, AnchorLon: anchorLon,
		StartZoom: startZoom, EndZoom: endZoom, SendZooms: sendZooms,
		Group: group, Strategy: strategy, Requested: compositionChoice,
		Camera: cameraAnimation, Label: labelAnimation,
		Composition: mapAnimation, Fallback: mapFallbackReason, Preset: presetID,
		Basemap: basemapStyle, Territory: territoryFeature, Country: countryFeature,
		Timeline: timelineLabel, Mountain: mountainAsset, Scope: territoryScope,
		MapLevel: mapLevel,
	}, nil
}

// mapTerritoryForFirstPin resolves the territory shapeName the composition was
// admitted for. Empty means the catalog cannot cover the pin.
func mapTerritoryForFirstPin(item capmodel.OverlayItem) (feature, scope string) {
	if len(item.Map.Pins) == 0 {
		return "", ""
	}
	pin := item.Map.Pins[0]
	scope = pin.Scope
	if capoverlay.MapTerritoryFeatureScopeCompatible(pin.Scope) {
		if resolved, ok := capoverlay.ResolveMapTerritoryFeature(capoverlay.MapTerritoryCatalogDir(), pin.Latitude, pin.Longitude); ok {
			feature = resolved
		}
	}
	return feature, scope
}

// payload encodes the resolved presentation as the native renderer's wire
// input. Pin timing is grounded DATA: the report carries it (Places), but the
// base flyover presents every place continuously and the renderer refuses
// non-empty spoken schedules — declared by the caller.
func (p mapRenderPresentation) payload(plan capmodel.OverlayPlan, renderWidth, renderHeight int, sampler string, profile capoverlay.MapEncodeProfile) map[string]any {
	item := plan.Items[0]
	payload := map[string]any{
		"width": renderWidth, "height": renderHeight,
		"fps_num": plan.FPSNum, "fps_den": plan.FPSDen,
		"duration_us": capoverlay.MapItemDurationMS(item, plan.DurationMS) * 1000, "pins": p.Pins,
		"camera_anchor":        []float64{p.AnchorLat, p.AnchorLon},
		"pin_timing":           []map[string]any{},
		"map_group":            p.Group,
		"map_group_strategy":   p.Strategy,
		"composition_family":   string(mapCompositionFamily(p.Composition)),
		"composition_fallback": p.Fallback,
		"area_glow_radius_km":  0,
		"camera_animation":     p.Camera,
		"label_animation":      p.Label,
		"map_animation":        p.Composition,
		"basemap":              p.Basemap,
		"typography":           dynamicMapTypography(item.Params),
		"encoder_profile": map[string]any{
			"codec": profile.Codec, "gpu": profile.ExpectGPU,
			"preset": profile.Preset, "quality": profile.Quality,
		},
		"sampler": sampler,
	}
	if p.Preset != "" {
		payload["map_preset"] = p.Preset
	}
	if p.SendZooms {
		payload["camera_start_zoom"] = p.StartZoom
		payload["camera_end_zoom"] = p.EndZoom
	}
	// The requested cartographic level owns the scale envelope: continent,
	// country, region and municipality are four visibly different views of the
	// same real basemap, not a label swap. It is sent as data too, so the wire
	// and the decision report keep the caller's intent. A level is an envelope,
	// not permission to crop: for a multi-place frame the fit zoom is the
	// tightest zoom that keeps every drawn pin inside the canvas, so a level
	// that zooms past it would hide a place. A wider level remains valid and is
	// kept; an incompatible one falls back to the fit interval.
	if p.MapLevel != "" {
		if start, end, ok := mapLevelCameraZooms(p.MapLevel); ok {
			if p.SendZooms && len(p.Pins) > 1 && p.EndZoom > 0 && end > p.EndZoom {
				start, end = p.StartZoom, p.EndZoom
			}
			if start > end {
				start = end
			}
			payload["map_level"] = p.MapLevel
			payload["camera_start_zoom"] = start
			payload["camera_end_zoom"] = end
		}
	}
	// Native-payload DATA fields (parity landed): the wire carries exactly
	// what the composition was admitted for — the renderer validates them at
	// its boundary and refuses silently-absent requirements.
	if capoverlay.MapTerritoryOverlayRequired(p.Composition) {
		payload["territory_feature"] = p.Territory
	}
	if p.Composition == "continental_atlas" || p.Composition == "historic_location_timeline" {
		payload["country_feature"] = p.Country
	}
	if p.Composition == "historic_location_timeline" {
		payload["timeline_label"] = p.Timeline
	}
	if p.Composition == "mountain_ridge_callout" {
		payload["mountain_overlay_asset"] = p.Mountain
	}
	if p.Composition == "area_glow_pulse" {
		// The admission gate guarantees a positive radius for this composition.
		payload["area_glow_radius_km"] = item.Map.AreaGlowRadiusKM
	}
	return payload
}
