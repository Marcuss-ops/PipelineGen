package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

const mapRunStyleSlot = "run"

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

// renderDynamicMapVideo delegates map motion to the same ChrononTemplate
// camera/tile-pyramid renderer used by approved dynamic-map overlays. The
// resulting MP4 enters normal content-addressed staging and publication.
func renderDynamicMapVideo(ctx context.Context, plan capoverlay.OverlayPlan, cacheDirs ...string) (string, error) {
	if len(plan.Items) != 1 || plan.Items[0].Map == nil {
		return "", fmt.Errorf("expected exactly one map item")
	}
	// The execution profile (codec + GPU expectation) is DECLARED up front:
	// production NVENC by default, certified CPU only under an explicit
	// operator opt-in. Telemetry and the upscale step below must both match
	// this profile — never a silent substitution.
	encodeProfile, err := scriptoverlay.ResolveMapEncodeProfile(os.Getenv(scriptoverlay.MapEncodeProfileEnv))
	if err != nil {
		return "", err
	}
	plateGenerator := strings.TrimSpace(os.Getenv("VELOX_GEO_MAP_PLATE_GENERATOR_PATH"))
	if plateGenerator == "" {
		return "", fmt.Errorf("VELOX_GEO_MAP_PLATE_GENERATOR_PATH is not configured")
	}
	scriptPath := filepath.Join(filepath.Dir(plateGenerator), "map", "render_dynamic_map_overlay.py")
	if _, err := os.Stat(scriptPath); err != nil {
		return "", fmt.Errorf("find Chronon map renderer %s: %w", scriptPath, err)
	}
	tmpDir, err := os.MkdirTemp("", "pipelinegen-dynamic-map-")
	if err != nil {
		return "", fmt.Errorf("create map render workspace: %w", err)
	}
	inputPath := filepath.Join(tmpDir, "map.json")
	outputPath := filepath.Join(tmpDir, "map.mp4")
	encodedOutputPath := filepath.Join(tmpDir, "map-encoded.mp4")
	summaryPath := filepath.Join(tmpDir, "map.telemetry.json")
	// Tile preparation and per-frame compositing scale roughly with canvas area.
	// Render the satellite plates at delivery resolution, then upscale the
	// finished map to the overlay contract. The final program is 1920x1080, so
	// retaining a 3x map raster only multiplies tile and RAM use before it is
	// downsampled again.
	renderWidth, renderHeight := plan.Width, plan.Height
	if renderWidth > 1920 || renderHeight > 1080 {
		renderWidth, renderHeight = 1920, 1080
	}
	// Optional per-pin composition extras (subtitle, category, color, value,
	// unit, photo, location) ride on the map item params keyed by pin id.
	// Absent extras degrade gracefully in the renderer: compact cards, no
	// counter row, no photo slot. Never fabricated.
	pinExtrasByID := map[string]any{}
	if raw, ok := plan.Items[0].Params["map_pin_extras"]; ok {
		if typed, ok := raw.(map[string]any); ok {
			pinExtrasByID = typed
		}
	}
	pins := make([]map[string]any, 0, len(plan.Items[0].Map.Pins))
	for _, pin := range plan.Items[0].Map.Pins {
		entry := map[string]any{"id": pin.ID, "label": pin.Label, "latitude": pin.Latitude, "longitude": pin.Longitude, "scope": pin.Scope}
		if color := strings.TrimSpace(pin.Color); color != "" {
			entry["color"] = color
		}
		if raw, ok := pinExtrasByID[pin.ID].(map[string]any); ok {
			for _, key := range []string{"subtitle", "category", "color", "value", "unit", "photo", "location"} {
				if value, ok := raw[key]; ok {
					entry[key] = value
				}
			}
		}
		pins = append(pins, entry)
	}
	// The camera move and basemap are the map's LOOK. They
	// are resolved by the pipeline's single deterministic sampler, seeded by the
	// plan identity: replaying a run reproduces the exact same map presentation,
	// while a new run (a new fingerprint) re-rolls it. A random draw here would
	// make a re-render of an already approved job silently different from the
	// artifact that was approved.
	// Per-pin spoken timing and the planner's group decision ride the item
	// params (map_pin_timing / map_group / map_group_strategy) and enter the
	// renderer payload so the camera activates each place exactly when the
	// voiceover names it. The renderer never re-derives editorial timing.
	pinTiming := scriptoverlay.MapPinTimingPayload(plan.Items[0].Params, pins)
	mapGroup := scriptoverlay.MapGroupPayload(plan.Items[0].Params)
	mapGroupStrategy := scriptoverlay.MapGroupStrategyParam(plan.Items[0].Params)
	seed := mapRunSeed(plan)
	// Select the map treatment once per map ITEM, not once per parent run.
	// A video holding several maps can vary compositions and basemaps instead
	// of repeating the same treatment on every map clip.
	// The seed is still the run identity, so replaying a run reproduces the
	// exact same per-item presentation; only the slot varies per item.
	mapStyleSlot := strings.TrimSpace(plan.Items[0].ID)
	if mapStyleSlot == "" {
		mapStyleSlot = mapRunStyleSlot
	}
	cameraChoice, _ := plan.Items[0].Params["map_camera_animation"].(string)
	labelChoice, _ := plan.Items[0].Params["map_label_animation"].(string)
	cameraAnimation, err := resolveMapAnimation(cameraChoice, seed, mapStyleSlot, "map_camera", mapCameraAnimationsCertified(), mapCameraAnimations)
	if err != nil {
		return "", err
	}
	labelAnimation, err := resolveMapAnimation(labelChoice, seed, mapStyleSlot, "map_label", mapLabelAnimationsCertified(), capoverlay.AnimationRandomCandidates("map.label"))
	if err != nil {
		return "", err
	}
	compositionChoice, _ := plan.Items[0].Params["map_animation"].(string)
	mapAnimation, mapFallbackReason, err := resolveMapCompositionFor(compositionChoice, seed, mapStyleSlot, mapCompositionInput{
		PinCount:   len(plan.Items[0].Map.Pins),
		Pins:       pins,
		DurationUS: scriptoverlay.MapItemDurationMS(plan.Items[0], plan.DurationMS) * 1000,
		Strategy:   mapGroupStrategy,
		Camera:     cameraAnimation,
		Label:      labelAnimation,
	})
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(mapAnimation, "ed_") {
		// This scene owns its name rail and marker inside the GPU compositor.
		labelAnimation = "none"
	}
	mapType, _ := plan.Items[0].Params["map_type"].(string)
	basemapStyle, err := resolveMapBasemap(mapType, seed, mapStyleSlot)
	if err != nil {
		return "", err
	}
	if cameraAnimation == "" || labelAnimation == "" || basemapStyle == "" {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("dynamic map presentation catalog is empty")
	}
	// Map Decision Report: the ONE per-map record of every decision behind this
	// render — places, exclusions, composition, seed, backend, fallback and
	// phase timings — so an unexpected map is explainable without chasing logs
	// across three repositories.
	report := scriptoverlay.NewMapDecisionReport(plan.PlanID, plan.Items[0].ID, seed)
	report.Requested = compositionChoice
	report.Composition = mapAnimation
	report.CompositionFamily = string(mapCompositionFamily(mapAnimation))
	report.Fallback = mapFallbackReason
	report.CameraAnimation = cameraAnimation
	report.LabelAnimation = labelAnimation
	report.Basemap = basemapStyle
	report.MapGroup = mapGroup
	report.Strategy = mapGroupStrategy
	if backend, backendReason, backendErr := scriptoverlay.ResolveMapItemBackend(plan.Items[0], os.Getenv("PIPELINEGEN_MAP_NATIVE_RENDER") == "1"); backendErr == nil {
		report.Backend = string(backend)
		report.BackendReason = backendReason
	} else {
		return "", backendErr
	}
	for _, pin := range pins {
		id, _ := pin["id"].(string)
		latitude, _ := scriptoverlay.MapPinFloat(pin, "latitude")
		longitude, _ := scriptoverlay.MapPinFloat(pin, "longitude")
		label, _ := pin["label"].(string)
		scope, _ := pin["scope"].(string)
		place := scriptoverlay.MapDecisionPlace{
			EntityID: id, Label: label, Scope: scope,
			Latitude: latitude, Longitude: longitude,
			Geocoded: true, State: "eligible",
		}
		for _, entry := range pinTiming {
			if entryID, _ := entry["id"].(string); entryID == id {
				place.StartUS = int64(scriptoverlay.MustFloat(entry["start_s"]) * 1_000_000)
				place.DurationUS = int64(scriptoverlay.MustFloat(entry["duration_s"]) * 1_000_000)
			}
		}
		if place.DurationUS == 0 {
			report.AddExclusion("no_spoken_timing", "pin "+id+" has no grounded occurrence timing; the camera cannot follow it")
		} else {
			place.State = "scheduled"
		}
		report.Places = append(report.Places, place)
	}
	if mapFallbackReason != "" {
		report.AddExclusion("declared_fallback", mapFallbackReason)
	}
	log.Printf("Chronon dynamic map styles selected: seed=%s camera=%s label=%s composition=%s basemap=%s", seed, cameraAnimation, labelAnimation, mapAnimation, basemapStyle)
	payload := map[string]any{
		"width": renderWidth, "height": renderHeight,
		"fps_num": plan.FPSNum, "fps_den": plan.FPSDen,
		"duration_us": scriptoverlay.MapItemDurationMS(plan.Items[0], plan.DurationMS) * 1000, "pins": pins,
		"pin_timing":           pinTiming,
		"map_group":            mapGroup,
		"map_group_strategy":   mapGroupStrategy,
		"composition_family":   string(mapCompositionFamily(mapAnimation)),
		"composition_fallback": mapFallbackReason,
		"area_glow_radius_km":  plan.Items[0].Map.AreaGlowRadiusKM,
		"camera_animation":     cameraAnimation,
		"label_animation":      labelAnimation,
		"map_animation":        mapAnimation,
		"basemap":              basemapStyle,
		"typography":           dynamicMapTypography(plan.Items[0].Params),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("encode map render input: %w", err)
	}
	if err := os.WriteFile(inputPath, encoded, 0600); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("write map render input: %w", err)
	}
	rendererFingerprint, err := dynamicMapRendererFingerprint(scriptPath, plateGenerator)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("identify dynamic map renderer dependencies: %w", err)
	}
	cacheKey := dynamicMapVideoCacheKey(encoded, rendererFingerprint, encodeProfile, plan.Width, plan.Height)
	renderStarted := time.Now()
	telemetryBytes, cacheHit := loadDynamicMapVideoCache(cacheKey, outputPath, cacheDirs...)
	if !cacheHit {
		cmd := exec.CommandContext(ctx, "/usr/bin/python3", scriptPath, "--input", inputPath,
			"--output", outputPath, "--summary-output", summaryPath)
		var output boundedMapRenderOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Run(); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("dynamic map renderer failed: %w: %s", err, strings.TrimSpace(output.String()))
		}
		if _, err := os.Stat(outputPath); err != nil {
			_ = os.RemoveAll(tmpDir)
			return "", fmt.Errorf("dynamic map renderer produced no video: %w: %s", err, strings.TrimSpace(output.String()))
		}
		telemetryBytes, err = os.ReadFile(summaryPath)
		if err != nil {
			return "", fmt.Errorf("read Chronon map telemetry summary: %w", err)
		}
	} else {
		log.Printf("Chronon dynamic map cache hit: key=%s", cacheKey)
		report.RecordPhase("artifact_cache_hit", 1)
		if err := os.WriteFile(summaryPath, telemetryBytes, 0600); err != nil {
			return "", fmt.Errorf("materialize cached Chronon map telemetry: %w", err)
		}
	}
	var summary struct {
		Schema     string `json:"schema"`
		Basemap    string `json:"basemap"`
		Frames     int    `json:"frames"`
		Dimensions struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"dimensions"`
		FPS struct {
			Num int `json:"num"`
			Den int `json:"den"`
		} `json:"fps"`
		RendererWallS        float64 `json:"renderer_wall_s"`
		FramePipelineS       float64 `json:"frame_pipeline_s"`
		RenderEncodeWallS    float64 `json:"render_encode_wall_s"`
		PostFrameTailS       float64 `json:"post_frame_tail_s"`
		EngineFallbackFrames int     `json:"engine_fallback_frames"`
		OutputBytes          int64   `json:"output_bytes"`
		VideoEncoder         string  `json:"video_encoder"`
		GPUEncoder           bool    `json:"gpu_encoder"`
		GateMS               float64 `json:"gate_ms"`
		Tile                 struct {
			DiskCacheHits           int     `json:"tile_disk_cache_hits"`
			DiskCacheMisses         int     `json:"tile_disk_cache_misses"`
			NetworkFetches          int     `json:"tile_network_fetches"`
			Fallbacks               int     `json:"tile_fallbacks"`
			BytesDownloaded         int64   `json:"tile_bytes_downloaded"`
			FetchMS                 float64 `json:"tile_fetch_ms"`
			PrefetchMS              float64 `json:"prefetch_ms"`
			PrefetchTilesRequested  int     `json:"prefetch_tiles_requested"`
			PrefetchTilesMemoryHits int     `json:"prefetch_tiles_memory_hits"`
			LateFetches             int     `json:"late_tile_fetches"`
			RenderedLocally         int     `json:"tile_rendered_locally"`
		} `json:"tile"`
		Plates struct {
			PrepareMS       float64 `json:"prepare_ms"`
			ComposeMS       float64 `json:"plate_compose_ms"`
			PlateCount      int     `json:"plate_count"`
			PlateBytes      int64   `json:"plate_bytes"`
			RequiredTiles   int     `json:"required_tile_count"`
			PrefetchFetched int     `json:"prefetch_tiles_fetched"`
			MemoryHits      int     `json:"prefetch_tiles_memory_hits"`
		} `json:"plates"`
	}
	if err := json.Unmarshal(telemetryBytes, &summary); err != nil {
		return "", fmt.Errorf("decode Chronon map telemetry summary: %w", err)
	}
	if summary.Schema != "chronon.dynamic-map-telemetry.v1" || summary.Frames < 2 || summary.OutputBytes <= 0 {
		return "", fmt.Errorf("dynamic map telemetry summary violates schema/output invariants: schema=%q frames=%d bytes=%d",
			summary.Schema, summary.Frames, summary.OutputBytes)
	}
	if summary.Dimensions.Width <= 0 || summary.Dimensions.Height <= 0 || summary.FPS.Num <= 0 || summary.FPS.Den <= 0 ||
		summary.Tile.DiskCacheHits+summary.Tile.DiskCacheMisses == 0 ||
		summary.Plates.RequiredTiles == 0 || summary.Plates.PlateCount == 0 ||
		summary.Plates.PrepareMS < 0 || summary.Plates.ComposeMS < 0 || summary.GateMS < 0 ||
		summary.FramePipelineS <= 0 || summary.RenderEncodeWallS <= 0 || summary.OutputBytes <= 0 {
		return "", fmt.Errorf("dynamic map telemetry summary is missing required telemetry field")
	}
	if summary.Dimensions.Width != renderWidth || summary.Dimensions.Height != renderHeight ||
		summary.FPS.Num != plan.FPSNum || summary.FPS.Den != plan.FPSDen {
		return "", fmt.Errorf("dynamic map telemetry media contract mismatch: dimensions=%dx%d fps=%d/%d, want %dx%d fps=%d/%d",
			summary.Dimensions.Width, summary.Dimensions.Height, summary.FPS.Num, summary.FPS.Den,
			renderWidth, renderHeight, plan.FPSNum, plan.FPSDen)
	}
	if err := scriptoverlay.ValidateMapEncoderTelemetry(summary.VideoEncoder, summary.GPUEncoder, encodeProfile); err != nil {
		return "", err
	}
	if renderWidth != plan.Width || renderHeight != plan.Height {
		scale := fmt.Sprintf("scale=%d:%d:flags=lanczos", plan.Width, plan.Height)
		upscaleArgs := []string{"/usr/bin/ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-i", outputPath, "-vf", scale, "-r", fmt.Sprintf("%d/%d", plan.FPSNum, plan.FPSDen)}
		upscaleArgs = append(upscaleArgs, scriptoverlay.MapEncodeVideoArgs(encodeProfile)...)
		upscaleArgs = append(upscaleArgs, "-pix_fmt", "yuv420p", "-movflags", "+faststart", encodedOutputPath)
		upscale := exec.CommandContext(ctx, upscaleArgs[0], upscaleArgs[1:]...)
		if output, err := upscale.CombinedOutput(); err != nil {
			return "", fmt.Errorf("scale dynamic map to overlay contract: %w: %s", err, strings.TrimSpace(string(output)))
		}
		if err := os.Rename(encodedOutputPath, outputPath); err != nil {
			return "", fmt.Errorf("install scaled dynamic map: %w", err)
		}
	}
	// Every cache miss is accounted for: an Esri raster miss is either
	// downloaded or a fallback; an offline vector-plate miss is drawn
	// locally. Locally-rendered tiles are admitted only for the editorial
	// basemap, so a raster provider can never hide missing downloads.
	if summary.Tile.RenderedLocally < 0 || summary.Tile.NetworkFetches < 0 ||
		summary.Tile.Fallbacks < 0 ||
		(summary.Tile.RenderedLocally > 0 && summary.Basemap != "chronon_editorial") ||
		summary.Tile.DiskCacheMisses != summary.Tile.NetworkFetches+summary.Tile.Fallbacks+summary.Tile.RenderedLocally {
		return "", fmt.Errorf("dynamic map telemetry cache/fetch counters do not balance: hits=%d misses=%d downloads=%d fallbacks=%d rendered_locally=%d basemap=%q",
			summary.Tile.DiskCacheHits, summary.Tile.DiskCacheMisses, summary.Tile.NetworkFetches, summary.Tile.Fallbacks,
			summary.Tile.RenderedLocally, summary.Basemap)
	}
	if summary.Plates.RequiredTiles != summary.Plates.PrefetchFetched+summary.Plates.MemoryHits ||
		summary.Tile.PrefetchTilesRequested != summary.Plates.RequiredTiles ||
		summary.Tile.PrefetchTilesMemoryHits != summary.Plates.MemoryHits {
		return "", fmt.Errorf("dynamic map telemetry prefetch coverage mismatch: required=%d fetched=%d plate_hits=%d requested=%d tile_hits=%d",
			summary.Plates.RequiredTiles, summary.Plates.PrefetchFetched, summary.Plates.MemoryHits,
			summary.Tile.PrefetchTilesRequested, summary.Tile.PrefetchTilesMemoryHits)
	}
	if summary.EngineFallbackFrames != 0 || summary.Tile.Fallbacks != 0 || summary.Tile.LateFetches != 0 {
		return "", fmt.Errorf("dynamic map has unapproved fallback work: engine_frames=%d tile_fallbacks=%d late_fetches=%d",
			summary.EngineFallbackFrames, summary.Tile.Fallbacks, summary.Tile.LateFetches)
	}
	log.Printf("Chronon dynamic map ready: cache_hit=%t frames=%d tiles(cache_hit=%d cache_miss=%d downloaded=%d fallback=%d late=%d fetch_ms=%.1f prefetch_ms=%.1f) plates(count=%d bytes=%d compose_ms=%.1f prepare_ms=%.1f required_tiles=%d prefetched=%d) frames_s=%.2f encode_wall_s=%.2f encode_tail_s=%.2f output_bytes=%d process_wall_s=%.2f",
		cacheHit,
		summary.Frames, summary.Tile.DiskCacheHits, summary.Tile.DiskCacheMisses,
		summary.Tile.NetworkFetches, summary.Tile.Fallbacks, summary.Tile.LateFetches,
		summary.Tile.FetchMS, summary.Tile.PrefetchMS, summary.Plates.PlateCount,
		summary.Plates.PlateBytes, summary.Plates.ComposeMS, summary.Plates.PrepareMS,
		summary.Plates.RequiredTiles, summary.Plates.PrefetchFetched,
		summary.FramePipelineS, summary.RenderEncodeWallS, summary.PostFrameTailS,
		summary.OutputBytes, time.Since(renderStarted).Seconds())
	// Phase timings close the decision report; emission is one structured log
	// line always, plus a durable file when the operator configured a report
	// directory (a requested report artifact that cannot be written fails
	// closed, never silently missing).
	report.RecordPhase("render_wall_s", time.Since(renderStarted).Seconds())
	report.RecordPhase("frame_pipeline_s", summary.FramePipelineS)
	report.RecordPhase("render_encode_wall_s", summary.RenderEncodeWallS)
	report.RecordPhase("post_frame_tail_s", summary.PostFrameTailS)
	report.RecordPhase("gate_ms", summary.GateMS)
	report.RecordPhase("tile_prefetch_ms", summary.Tile.PrefetchMS)
	report.RecordPhase("plate_prepare_ms", summary.Plates.PrepareMS)
	report.RecordPhase("plate_compose_ms", summary.Plates.ComposeMS)
	// Same owner-measured phases, projected into the canonical operation
	// telemetry so the map cost is joinable to the run instead of living only
	// in the decision report and the worker log.
	if !cacheHit {
		projectMapRendererPhases(ctx, summary.Tile.PrefetchMS, summary.Plates.PrepareMS, summary.RenderEncodeWallS)
	}
	if _, err := report.JSON(); err != nil {
		return "", fmt.Errorf("encode map decision report: %w", err)
	}
	// Pre-render admission already passed if we reached the renderer; mark
	// scheduled pins and persist the durable report next to the artifact.
	// The report is written beside the rendered MP4 (same tmp workspace that
	// becomes the content-addressed artifact dir) AND to the operator dir when
	// configured — never log-only, so a wrong map is traceable to its job.
	for i := range report.Places {
		// Renderer confirmed frames: every eligible/scheduled pin was drawn.
		// Pins without spoken timing still render as markers (no camera
		// follow) — they are rendered, with the no_spoken_timing exclusion
		// explaining the degraded camera, not a missing pin.
		report.Places[i].State = "rendered"
		report.Places[i].Presented = true
	}
	report.RecordPhase("render_wall_s", time.Since(renderStarted).Seconds())
	// Re-encode after state promotion so the log line reflects rendered.
	encodedReport, err := report.JSON()
	if err != nil {
		return "", fmt.Errorf("encode map decision report: %w", err)
	}
	log.Printf("map decision report: %s", encodedReport)
	if _, err := scriptoverlay.WriteMapDecisionReport(tmpDir, report); err != nil {
		return "", fmt.Errorf("write map decision report: %w", err)
	}
	if reportDir := strings.TrimSpace(os.Getenv("PIPELINEGEN_MAP_REPORT_DIR")); reportDir != "" {
		if _, err := scriptoverlay.WriteMapDecisionReport(reportDir, report); err != nil {
			return "", fmt.Errorf("write map decision report: %w", err)
		}
	}
	if !cacheHit {
		if err := storeDynamicMapVideoCache(cacheKey, outputPath, telemetryBytes, cacheDirs...); err != nil {
			// The cache is optional; a write failure must not discard a valid
			// map artifact or turn a successful render into a failed job.
			log.Printf("Chronon dynamic map cache store failed: key=%s err=%v", cacheKey, err)
		}
	}
	return outputPath, nil
}

// resolveMapBasemap maps the runtime map_type through the shared map registry;
// random choices use the active basemap palette from the same registry.
func resolveMapBasemap(mapType, seed, slot string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(mapType), "random") {
		selected := deterministicMapStyle(seed, "map_basemap", slot, mapBasemapStyles())
		if selected == "" {
			return "", fmt.Errorf("no certified map basemap available")
		}
		return selected, nil
	}
	if id, ok := capoverlay.AnimationMapTypeBasemap(mapType); ok {
		return id, nil
	}
	return "", fmt.Errorf("unsupported runtime map_type %q", mapType)
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

func mapMapAnimationsRandomPool() []string {
	return scriptoverlay.MapCompositionRandomPool()
}

// mapCameraAnimationsCertified lists every camera id the Python renderer
// accepts, including explicit-only choices. resolveMapAnimation validates
// against this list; the random sampler draws from mapCameraAnimations.
func mapCameraAnimationsCertified() []string {
	return capoverlay.AnimationCandidates("map.camera")
}
