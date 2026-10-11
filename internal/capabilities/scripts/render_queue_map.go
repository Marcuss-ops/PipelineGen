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
)

const mapRunStyleSlot = "run"

// renderDynamicMapVideo delegates map motion to the same ChrononTemplate
// camera/tile-pyramid renderer used by approved dynamic-map overlays. The
// resulting MP4 enters normal content-addressed staging and publication.
func renderDynamicMapVideo(ctx context.Context, plan capoverlay.OverlayPlan) (string, error) {
	if len(plan.Items) != 1 || plan.Items[0].Map == nil {
		return "", fmt.Errorf("expected exactly one map item")
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
	pins := make([]map[string]any, 0, len(plan.Items[0].Map.Pins))
	for _, pin := range plan.Items[0].Map.Pins {
		pins = append(pins, map[string]any{"id": pin.ID, "label": pin.Label, "latitude": pin.Latitude, "longitude": pin.Longitude, "scope": pin.Scope})
	}
	// The camera move, the label accent and the basemap are the map's LOOK. They
	// are resolved by the pipeline's single deterministic sampler, seeded by the
	// plan identity: replaying a run reproduces the exact same map presentation,
	// while a new run (a new fingerprint) re-rolls it. A random draw here would
	// make a re-render of an already approved job silently different from the
	// artifact that was approved.
	seed := mapRunSeed(plan)
	// Select the map treatment once per map ITEM, not once per parent run.
	// A video holding several maps must show different camera moves, label
	// accents and basemaps instead of the same treatment on every map clip.
	// The seed is still the run identity, so replaying a run reproduces the
	// exact same per-item presentation; only the slot varies per item.
	mapStyleSlot := strings.TrimSpace(plan.Items[0].ID)
	if mapStyleSlot == "" {
		mapStyleSlot = mapRunStyleSlot
	}
	cameraChoice, _ := plan.Items[0].Params["map_camera_animation"].(string)
	labelChoice, _ := plan.Items[0].Params["map_label_animation"].(string)
	cameraAnimation, err := resolveMapAnimation(cameraChoice, seed, mapStyleSlot, "map_camera", mapCameraAnimations)
	if err != nil {
		return "", err
	}
	labelAnimation, err := resolveMapAnimation(labelChoice, seed, mapStyleSlot, "map_label", mapLabelAnimations)
	if err != nil {
		return "", err
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
	log.Printf("Chronon dynamic map styles selected: seed=%s camera=%s label=%s basemap=%s", seed, cameraAnimation, labelAnimation, basemapStyle)
	payload := map[string]any{
		"width": renderWidth, "height": renderHeight,
		"fps_num": plan.FPSNum, "fps_den": plan.FPSDen,
		"duration_us": mapItemDurationMS(plan.Items[0], plan.DurationMS) * 1000, "pins": pins,
		"area_glow_radius_km": plan.Items[0].Map.AreaGlowRadiusKM,
		"camera_animation":    cameraAnimation,
		"label_animation":     labelAnimation,
		"basemap":             basemapStyle,
		"typography":          dynamicMapTypography(plan.Items[0].Params),
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
	cmd := exec.CommandContext(ctx, "/usr/bin/python3", scriptPath, "--input", inputPath,
		"--output", outputPath, "--summary-output", summaryPath)
	var output boundedMapRenderOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	renderStarted := time.Now()
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("Chronon map renderer failed: %w: %s", err, strings.TrimSpace(output.String()))
	}
	if _, err := os.Stat(outputPath); err != nil {
		_ = os.RemoveAll(tmpDir)
		return "", fmt.Errorf("Chronon map renderer produced no video: %w: %s", err, strings.TrimSpace(output.String()))
	}
	var summary struct {
		Schema     string `json:"schema"`
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
	summaryBytes, err := os.ReadFile(summaryPath)
	if err != nil {
		return "", fmt.Errorf("read Chronon map telemetry summary: %w: %s", err, strings.TrimSpace(output.String()))
	}
	if err := json.Unmarshal(summaryBytes, &summary); err != nil {
		return "", fmt.Errorf("decode Chronon map telemetry summary: %w", err)
	}
	if summary.Schema != "chronon.dynamic-map-telemetry.v1" || summary.Frames < 2 || summary.OutputBytes <= 0 {
		return "", fmt.Errorf("Chronon map telemetry summary violates schema/output invariants: schema=%q frames=%d bytes=%d",
			summary.Schema, summary.Frames, summary.OutputBytes)
	}
	if summary.Dimensions.Width <= 0 || summary.Dimensions.Height <= 0 || summary.FPS.Num <= 0 || summary.FPS.Den <= 0 ||
		summary.Tile.DiskCacheHits+summary.Tile.DiskCacheMisses == 0 ||
		summary.Plates.RequiredTiles == 0 || summary.Plates.PlateCount == 0 ||
		summary.Plates.PrepareMS < 0 || summary.Plates.ComposeMS < 0 || summary.GateMS < 0 ||
		summary.FramePipelineS <= 0 || summary.RenderEncodeWallS <= 0 || summary.OutputBytes <= 0 {
		return "", fmt.Errorf("Chronon map telemetry summary is missing required telemetry field")
	}
	if summary.Dimensions.Width != renderWidth || summary.Dimensions.Height != renderHeight ||
		summary.FPS.Num != plan.FPSNum || summary.FPS.Den != plan.FPSDen {
		return "", fmt.Errorf("Chronon map telemetry media contract mismatch: dimensions=%dx%d fps=%d/%d, want %dx%d fps=%d/%d",
			summary.Dimensions.Width, summary.Dimensions.Height, summary.FPS.Num, summary.FPS.Den,
			renderWidth, renderHeight, plan.FPSNum, plan.FPSDen)
	}
	if !summary.GPUEncoder || summary.VideoEncoder != "h264_nvenc" {
		return "", fmt.Errorf("Chronon map renderer did not use the required GPU encoder: encoder=%q gpu=%t",
			summary.VideoEncoder, summary.GPUEncoder)
	}
	if renderWidth != plan.Width || renderHeight != plan.Height {
		scale := fmt.Sprintf("scale=%d:%d:flags=lanczos", plan.Width, plan.Height)
		upscale := exec.CommandContext(ctx, "/usr/bin/ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
			"-i", outputPath, "-vf", scale, "-r", fmt.Sprintf("%d/%d", plan.FPSNum, plan.FPSDen),
			"-c:v", "h264_nvenc", "-gpu", "0", "-preset", "p4", "-tune", "hq", "-rc", "vbr", "-cq", "18", "-b:v", "0",
			"-pix_fmt", "yuv420p", "-movflags", "+faststart", encodedOutputPath)
		if output, err := upscale.CombinedOutput(); err != nil {
			return "", fmt.Errorf("scale dynamic map to overlay contract: %w: %s", err, strings.TrimSpace(string(output)))
		}
		if err := os.Rename(encodedOutputPath, outputPath); err != nil {
			return "", fmt.Errorf("install scaled dynamic map: %w", err)
		}
	}
	if summary.Tile.DiskCacheMisses != summary.Tile.NetworkFetches+summary.Tile.Fallbacks {
		return "", fmt.Errorf("Chronon map telemetry cache/fetch counters do not balance: hits=%d misses=%d downloads=%d fallbacks=%d",
			summary.Tile.DiskCacheHits, summary.Tile.DiskCacheMisses, summary.Tile.NetworkFetches, summary.Tile.Fallbacks)
	}
	if summary.Plates.RequiredTiles != summary.Plates.PrefetchFetched+summary.Plates.MemoryHits ||
		summary.Tile.PrefetchTilesRequested != summary.Plates.RequiredTiles ||
		summary.Tile.PrefetchTilesMemoryHits != summary.Plates.MemoryHits {
		return "", fmt.Errorf("Chronon map telemetry prefetch coverage mismatch: required=%d fetched=%d plate_hits=%d requested=%d tile_hits=%d",
			summary.Plates.RequiredTiles, summary.Plates.PrefetchFetched, summary.Plates.MemoryHits,
			summary.Tile.PrefetchTilesRequested, summary.Tile.PrefetchTilesMemoryHits)
	}
	if summary.EngineFallbackFrames != 0 || summary.Tile.Fallbacks != 0 || summary.Tile.LateFetches != 0 {
		return "", fmt.Errorf("Chronon map has unapproved fallback work: engine_frames=%d tile_fallbacks=%d late_fetches=%d",
			summary.EngineFallbackFrames, summary.Tile.Fallbacks, summary.Tile.LateFetches)
	}
	log.Printf("Chronon dynamic map ready: frames=%d tiles(cache_hit=%d cache_miss=%d downloaded=%d fallback=%d late=%d fetch_ms=%.1f prefetch_ms=%.1f) plates(count=%d bytes=%d compose_ms=%.1f prepare_ms=%.1f required_tiles=%d prefetched=%d) frames_s=%.2f encode_wall_s=%.2f encode_tail_s=%.2f output_bytes=%d process_wall_s=%.2f",
		summary.Frames, summary.Tile.DiskCacheHits, summary.Tile.DiskCacheMisses,
		summary.Tile.NetworkFetches, summary.Tile.Fallbacks, summary.Tile.LateFetches,
		summary.Tile.FetchMS, summary.Tile.PrefetchMS, summary.Plates.PlateCount,
		summary.Plates.PlateBytes, summary.Plates.ComposeMS, summary.Plates.PrepareMS,
		summary.Plates.RequiredTiles, summary.Plates.PrefetchFetched,
		summary.FramePipelineS, summary.RenderEncodeWallS, summary.PostFrameTailS,
		summary.OutputBytes, time.Since(renderStarted).Seconds())
	return outputPath, nil
}

func resolveMapBasemap(mapType, seed, slot string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mapType)) {
	case "", "roadmap":
		return "esri_light", nil
	case "terrain":
		return "esri_topo", nil
	case "satellite":
		return "esri_sat", nil
	case "random":
		return deterministicMapStyle(seed, "map_basemap", slot, mapBasemapStyles()), nil
	default:
		return "", fmt.Errorf("unsupported runtime map_type %q", mapType)
	}
}

func resolveMapAnimation(requested, seed, slot, channel string, certified []string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == "random" {
		selected := deterministicMapStyle(seed, channel, slot, certified)
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
	style := map[string]any{
		"font_family": "bricolage_grotesque", "font_size_px": 92.0,
		"fill": "#F8F5EA", "stroke_size": 2.0,
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

func mapItemDurationMS(item capoverlay.OverlayItem, fallback int64) int64 {
	if item.StartMs >= 0 && item.EndMs > item.StartMs {
		return item.EndMs - item.StartMs
	}
	return fallback
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

// mapBasemapStyles is the certified palette available when a caller explicitly
// asks for map_type=random. The default map_type is the stable street-map style.
// TestMapBasemapStylesMatchChrononPalette guards cross-repository drift.
func mapBasemapStyles() []string {
	return []string{"esri_light", "esri_topo", "esri_sat"}
}

// mapCameraAnimations and mapLabelAnimations are the certified presentation
// vocabularies the Chronon map renderer accepts for a generated map
// (render_dynamic_map_overlay.py validates camera_animation against its own set
// and label_animation against MAP_LABEL_ANIMATIONS). They are only candidate
// lists: the choice among them belongs to the deterministic sampler below.
var (
	// Map camera treatments. Signature Dive starts at a much wider zoom and
	// reads as a sudden drop on city maps, so it stays out. Tilt Reveal and
	// Orbit Arrival add pitch/yaw variety through the certified _ShotBuilder
	// presets (the Python renderer already accepts them); combined with
	// per-item sampling below, one video holds visibly different map moves.
	mapCameraAnimations = []string{"slow_approach", "wide_context", "tilt_reveal", "orbit_arrival"}
	mapLabelAnimations  = []string{"gentle_fade", "soft_glow", "clean_fade", "word_soft_fade", "slow_fade", "quiet_bloom", "quick_fade", "silky_fade", "subtle_halo", "cinematic_fade"}
)

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
// the pipeline. The channel keeps the three choices independent: the same seed
// picks the camera move, the label accent and the basemap from separate
// digests, so they never collapse onto one correlated draw.
func deterministicMapStyle(seed, channel, itemID string, options []string) string {
	return capoverlay.DefaultDeterministicPresetSampler.Sample(capoverlay.PresetSampleInput{
		JobFingerprint: seed,
		SceneID:        "map",
		SemanticID:     itemID,
		PresetFamily:   channel,
		Presets:        options,
	}).Preset
}
