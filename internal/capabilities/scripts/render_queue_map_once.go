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
	"time"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/overlay"
)

func renderDynamicMapVideoOnce(ctx context.Context, plan capoverlay.OverlayPlan, gate DynamicMapGPUAdmission, cacheDirs ...string) (string, error) {
	profile, err := scriptoverlay.ResolveMapEncodeProfile(os.Getenv(scriptoverlay.MapEncodeProfileEnv))
	if err != nil {
		return "", err
	}
	if len(plan.Items) != 1 || plan.Items[0].Map == nil {
		return "", fmt.Errorf("expected exactly one map item")
	}
	// The execution profile (codec + GPU expectation) is DECLARED up front:
	// production NVENC by default, certified CPU only under an explicit
	// operator opt-in. Telemetry and the upscale step below must both match
	// this profile — never a silent substitution.
	encodeProfile := profile
	sampler := dynamicMapSamplerForProfile(encodeProfile)
	mapAnimationForAdmission, _ := plan.Items[0].Params["map_animation"].(string)
	needsGPUAdmission := mapRenderRequiresGPU(encodeProfile, sampler, mapAnimationForAdmission)
	// Single map lane: the native renderer of ChrononPlugin (the Python
	// renderer is retired — TICKET-MAPS-NATIVE-PORT fase 7).
	nativeRenderer := strings.TrimSpace(os.Getenv("VELOX_MAP_NATIVE_RENDERER_PATH"))
	if nativeRenderer == "" {
		return "", fmt.Errorf("VELOX_MAP_NATIVE_RENDERER_PATH is not configured")
	}
	if _, err := os.Stat(nativeRenderer); err != nil {
		return "", fmt.Errorf("find Chronon native map renderer %s: %w", nativeRenderer, err)
	}
	tmpDir, err := os.MkdirTemp("", "pipelinegen-dynamic-map-")
	if err != nil {
		return "", fmt.Errorf("create map render workspace: %w", err)
	}
	// Workspace ownership: every error below releases the workspace, and the
	// single success transfers it to the caller (the fold removes it after the
	// import; the prepare releases it after storing the durable cache entry).
	keepWorkspace := false
	defer func() {
		if !keepWorkspace {
			_ = os.RemoveAll(tmpDir)
		}
	}()
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
		return "", fmt.Errorf("dynamic map presentation catalog is empty")
	}
	needsGPUAdmission = needsGPUAdmission || mapRenderRequiresGPU(encodeProfile, sampler, mapAnimation)
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
		"encoder_profile": map[string]any{
			"codec": encodeProfile.Codec, "gpu": encodeProfile.ExpectGPU,
			"preset": encodeProfile.Preset, "quality": encodeProfile.Quality,
		},
		"sampler": sampler,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode map render input: %w", err)
	}
	if err := os.WriteFile(inputPath, encoded, 0600); err != nil {
		return "", fmt.Errorf("write map render input: %w", err)
	}
	rendererFingerprint, err := dynamicMapRendererFingerprint(nativeRenderer)
	if err != nil {
		return "", fmt.Errorf("identify dynamic map renderer dependencies: %w", err)
	}
	cacheKey := dynamicMapVideoCacheKey(encoded, rendererFingerprint, encodeProfile, sampler, plan.Width, plan.Height)
	renderStarted := time.Now()
	telemetryBytes, videoSHA256, videoSize, cacheHit := loadDynamicMapVideoCacheVerified(cacheKey, outputPath, cacheDirs...)
	if !cacheHit && needsGPUAdmission {
		if gate == nil {
			return "", fmt.Errorf("dynamic map GPU work requires the shared RenderingGen GPU gate")
		}
		release, gateErr := gate.Acquire(ctx)
		if gateErr != nil {
			return "", fmt.Errorf("acquire shared RenderingGen GPU admission for dynamic map: %w", gateErr)
		}
		defer release()
	}
	if !cacheHit {
		cmd := exec.CommandContext(ctx, nativeRenderer, "--input", inputPath,
			"--output", outputPath, "--summary-output", summaryPath)
		var output boundedMapRenderOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("dynamic map renderer failed: %w: %s", err, strings.TrimSpace(output.String()))
		}
		if _, err := os.Stat(outputPath); err != nil {
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
		GPUMapSampler        string  `json:"gpu_map_sampler"`
		EncoderPreset        string  `json:"encoder_preset"`
		EncoderQuality       int     `json:"encoder_quality"`
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
	if err := scriptoverlay.ValidateMapEncoderTelemetry(summary.VideoEncoder, summary.GPUEncoder, summary.EncoderPreset, summary.EncoderQuality, encodeProfile); err != nil {
		return "", err
	}
	if summary.GPUMapSampler != sampler {
		return "", fmt.Errorf("dynamic map renderer used sampler=%q, but the declared render requires sampler=%q", summary.GPUMapSampler, sampler)
	}
	if !cacheHit && (renderWidth != plan.Width || renderHeight != plan.Height) {
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
	// The cache hit bypasses rendering and therefore has no GPU admission to
	// consume; every miss completes under the shared gate when required.
	// Mark scheduled pins and persist the durable report next to the artifact.
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
		var cacheErr error
		videoSHA256, videoSize, cacheErr = storeDynamicMapVideoCacheVerified(cacheKey, outputPath, telemetryBytes, cacheDirs...)
		if cacheErr != nil {
			// The cache is optional; a write failure must not discard a valid
			// map artifact or turn a successful render into a failed job.
			log.Printf("Chronon dynamic map cache store failed: key=%s err=%v", cacheKey, cacheErr)
		}
		// The cache helper hashes while streaming bytes into its atomic cache
		// copy. When caching is disabled, or a cache write failed before source
		// bytes were consumed, hash the final output once for the import attestation.
		if videoSHA256 == "" || videoSize <= 0 {
			var identityErr error
			videoSHA256, videoSize, identityErr = hashDynamicMapVideo(outputPath)
			if identityErr != nil {
				return "", fmt.Errorf("identify rendered dynamic map video: %w", identityErr)
			}
		}
	}
	if videoSHA256 == "" || videoSize <= 0 {
		return "", fmt.Errorf("dynamic map video identity is incomplete")
	}
	identityBytes, err := json.Marshal(dynamicMapVideoIdentityManifest{SHA256: videoSHA256, SizeBytes: videoSize})
	if err != nil {
		return "", fmt.Errorf("encode dynamic map video identity: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "map.video.identity.json"), identityBytes, 0o600); err != nil {
		return "", fmt.Errorf("write dynamic map video identity: %w", err)
	}
	keepWorkspace = true
	return outputPath, nil
}
