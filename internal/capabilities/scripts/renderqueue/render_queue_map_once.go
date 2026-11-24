package renderqueue

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
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// cameraMoveZoom reads one grounded zoom of the planner's camera move.
func cameraMoveZoom(move *capmodel.MapCameraMove, start bool) float64 {
	if move == nil {
		return 0
	}
	if start {
		return move.StartZoom
	}
	return move.EndZoom
}

// hasPinAccent reports whether any pin carries per-pin presentation the
// native base flyover does not draw (accent color), so the decision report
// declares the simplification once.
func hasPinAccent(pins []capmodel.MapOverlayPin) bool {
	for _, pin := range pins {
		if strings.TrimSpace(pin.Color) != "" {
			return true
		}
	}
	return false
}

// mapRenderWorkspace is the per-render private directory plus the file names
// the renderer and the telemetry contract use inside it.
type mapRenderWorkspace struct {
	dir      string
	input    string
	output   string
	encoded  string
	summary  string
	identity string
}

func newMapRenderWorkspace() (mapRenderWorkspace, error) {
	dir, err := os.MkdirTemp("", "pipelinegen-dynamic-map-")
	if err != nil {
		return mapRenderWorkspace{}, fmt.Errorf("create map render workspace: %w", err)
	}
	return mapRenderWorkspace{
		dir:      dir,
		input:    filepath.Join(dir, "map.json"),
		output:   filepath.Join(dir, "map.mp4"),
		encoded:  filepath.Join(dir, "map-encoded.mp4"),
		summary:  filepath.Join(dir, "map.telemetry.json"),
		identity: filepath.Join(dir, "map.video.identity.json"),
	}, nil
}

// nativeMapRendererPath resolves the single native map lane (the Python
// renderer is retired — TICKET-MAPS-NATIVE-PORT fase 7).
func nativeMapRendererPath() (string, error) {
	path := strings.TrimSpace(os.Getenv("VELOX_MAP_NATIVE_RENDERER_PATH"))
	if path == "" {
		return "", fmt.Errorf("VELOX_MAP_NATIVE_RENDERER_PATH is not configured")
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("find Chronon native map renderer %s: %w", path, err)
	}
	return path, nil
}

// mapRenderResolution keeps the renderer at delivery resolution. Tile
// preparation and per-frame compositing scale roughly with canvas area, and
// the final program is 1920x1080: retaining a 3x map raster would multiply
// tile and RAM use before it is downsampled again.
func mapRenderResolution(plan capmodel.OverlayPlan) (int, int) {
	renderWidth, renderHeight := plan.Width, plan.Height
	if renderWidth > kernelmedia.CanonicalWidth || renderHeight > kernelmedia.CanonicalHeight {
		renderWidth, renderHeight = kernelmedia.CanonicalWidth, kernelmedia.CanonicalHeight
	}
	return renderWidth, renderHeight
}

// mapRenderTelemetry is the renderer's own chronon.dynamic-map-telemetry.v1
// summary, projected onto the phases the map cost is accounted for.
type mapRenderTelemetry struct {
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

// mapRenderContract is the declared render this summary must confirm: its
// media geometry, its sampler, and the encoder profile it was admitted for.
type mapRenderContract struct {
	plan         capmodel.OverlayPlan
	renderWidth  int
	renderHeight int
	sampler      string
	profile      capoverlay.MapEncodeProfile
}

// decodeMapRenderTelemetry decodes the renderer summary and enforces the
// contract the render declared before it ran. A summary that does not confirm
// the declared media, sampler and encoder fails closed — the renderer never
// substitutes its backend silently.
func decodeMapRenderTelemetry(telemetryBytes []byte, contract mapRenderContract) (mapRenderTelemetry, error) {
	var summary mapRenderTelemetry
	if err := json.Unmarshal(telemetryBytes, &summary); err != nil {
		return mapRenderTelemetry{}, fmt.Errorf("decode Chronon map telemetry summary: %w", err)
	}
	if summary.Schema != "chronon.dynamic-map-telemetry.v1" || summary.Frames < 2 || summary.OutputBytes <= 0 {
		return mapRenderTelemetry{}, fmt.Errorf("dynamic map telemetry summary violates schema/output invariants: schema=%q frames=%d bytes=%d",
			summary.Schema, summary.Frames, summary.OutputBytes)
	}
	if summary.Dimensions.Width <= 0 || summary.Dimensions.Height <= 0 || summary.FPS.Num <= 0 || summary.FPS.Den <= 0 ||
		summary.Tile.DiskCacheHits+summary.Tile.DiskCacheMisses == 0 ||
		summary.Plates.RequiredTiles == 0 || summary.Plates.PlateCount == 0 ||
		summary.Plates.PrepareMS < 0 || summary.Plates.ComposeMS < 0 || summary.GateMS < 0 ||
		summary.FramePipelineS <= 0 || summary.RenderEncodeWallS <= 0 || summary.OutputBytes <= 0 {
		return mapRenderTelemetry{}, fmt.Errorf("dynamic map telemetry summary is missing required telemetry field")
	}
	if summary.Dimensions.Width != contract.renderWidth || summary.Dimensions.Height != contract.renderHeight ||
		summary.FPS.Num != contract.plan.FPSNum || summary.FPS.Den != contract.plan.FPSDen {
		return mapRenderTelemetry{}, fmt.Errorf("dynamic map telemetry media contract mismatch: dimensions=%dx%d fps=%d/%d, want %dx%d fps=%d/%d",
			summary.Dimensions.Width, summary.Dimensions.Height, summary.FPS.Num, summary.FPS.Den,
			contract.renderWidth, contract.renderHeight, contract.plan.FPSNum, contract.plan.FPSDen)
	}
	if err := capoverlay.ValidateMapEncoderTelemetry(summary.VideoEncoder, summary.GPUEncoder, summary.EncoderPreset, summary.EncoderQuality, contract.profile); err != nil {
		return mapRenderTelemetry{}, err
	}
	if summary.GPUMapSampler != contract.sampler {
		return mapRenderTelemetry{}, fmt.Errorf("dynamic map renderer used sampler=%q, but the declared render requires sampler=%q", summary.GPUMapSampler, contract.sampler)
	}
	return summary, nil
}

// validateMapRenderWork enforces the tile/plate accounting after the artifact
// exists: every cache miss is either downloaded, a fallback or drawn locally,
// locally-rendered tiles are admitted only for the editorial basemap (so a
// raster provider can never hide missing downloads), and prefetch covers
// exactly the required tiles. Any unapproved fallback work fails closed.
func validateMapRenderWork(summary mapRenderTelemetry) error {
	if summary.Tile.RenderedLocally < 0 || summary.Tile.NetworkFetches < 0 ||
		summary.Tile.Fallbacks < 0 ||
		(summary.Tile.RenderedLocally > 0 && summary.Basemap != "chronon_editorial") ||
		summary.Tile.DiskCacheMisses != summary.Tile.NetworkFetches+summary.Tile.Fallbacks+summary.Tile.RenderedLocally {
		return fmt.Errorf("dynamic map telemetry cache/fetch counters do not balance: hits=%d misses=%d downloads=%d fallbacks=%d rendered_locally=%d basemap=%q",
			summary.Tile.DiskCacheHits, summary.Tile.DiskCacheMisses, summary.Tile.NetworkFetches, summary.Tile.Fallbacks,
			summary.Tile.RenderedLocally, summary.Basemap)
	}
	if summary.Plates.RequiredTiles != summary.Plates.PrefetchFetched+summary.Plates.MemoryHits ||
		summary.Tile.PrefetchTilesRequested != summary.Plates.RequiredTiles ||
		summary.Tile.PrefetchTilesMemoryHits != summary.Plates.MemoryHits {
		return fmt.Errorf("dynamic map telemetry prefetch coverage mismatch: required=%d fetched=%d plate_hits=%d requested=%d tile_hits=%d",
			summary.Plates.RequiredTiles, summary.Plates.PrefetchFetched, summary.Plates.MemoryHits,
			summary.Tile.PrefetchTilesRequested, summary.Tile.PrefetchTilesMemoryHits)
	}
	if summary.EngineFallbackFrames != 0 || summary.Tile.Fallbacks != 0 || summary.Tile.LateFetches != 0 {
		return fmt.Errorf("dynamic map has unapproved fallback work: engine_frames=%d tile_fallbacks=%d late_fetches=%d",
			summary.EngineFallbackFrames, summary.Tile.Fallbacks, summary.Tile.LateFetches)
	}
	return nil
}

// executeMapRenderer runs the native map lane over the prepared wire input.
func executeMapRenderer(ctx context.Context, renderer string, workspace mapRenderWorkspace) error {
	cmd := exec.CommandContext(ctx, renderer, "--input", workspace.input,
		"--output", workspace.output, "--summary-output", workspace.summary)
	var output boundedMapRenderOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dynamic map renderer failed: %w: %s", err, strings.TrimSpace(output.String()))
	}
	if _, err := os.Stat(workspace.output); err != nil {
		return fmt.Errorf("dynamic map renderer produced no video: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return nil
}

// scaleMapRenderVideo upscales a delivery-resolution render to the overlay
// contract with the declared encoder profile.
func scaleMapRenderVideo(ctx context.Context, workspace mapRenderWorkspace, plan capmodel.OverlayPlan, profile capoverlay.MapEncodeProfile) error {
	scale := fmt.Sprintf("scale=%d:%d:flags=lanczos,setsar=1", plan.Width, plan.Height)
	args := []string{"/usr/bin/ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-i", workspace.output, "-vf", scale, "-r", fmt.Sprintf("%d/%d", plan.FPSNum, plan.FPSDen)}
	args = append(args, capoverlay.MapEncodeVideoArgs(profile)...)
	args = append(args, "-pix_fmt", kernelmedia.DefaultAssemblyMediaContractV2().PixelFormat, "-movflags", "+faststart", workspace.encoded)
	upscale := exec.CommandContext(ctx, args[0], args[1:]...)
	if output, err := upscale.CombinedOutput(); err != nil {
		return fmt.Errorf("scale dynamic map to overlay contract: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Rename(workspace.encoded, workspace.output); err != nil {
		return fmt.Errorf("install scaled dynamic map: %w", err)
	}
	return nil
}

// recordMapRenderPhases closes the decision report with the renderer's own
// measured phases, so the map cost is explained by its parts.
func recordMapRenderPhases(report *capoverlay.MapDecisionReport, summary mapRenderTelemetry, elapsed time.Duration) {
	report.RecordPhase("render_wall_s", elapsed.Seconds())
	report.RecordPhase("frame_pipeline_s", summary.FramePipelineS)
	report.RecordPhase("render_encode_wall_s", summary.RenderEncodeWallS)
	report.RecordPhase("post_frame_tail_s", summary.PostFrameTailS)
	report.RecordPhase("gate_ms", summary.GateMS)
	report.RecordPhase("tile_prefetch_ms", summary.Tile.PrefetchMS)
	report.RecordPhase("plate_prepare_ms", summary.Plates.PrepareMS)
	report.RecordPhase("plate_compose_ms", summary.Plates.ComposeMS)
}

// logMapRenderSummary emits the one-line operator record of the render.
func logMapRenderSummary(cacheHit bool, summary mapRenderTelemetry, elapsed time.Duration) {
	log.Printf("Chronon dynamic map ready: cache_hit=%t frames=%d tiles(cache_hit=%d cache_miss=%d downloaded=%d fallback=%d late=%d fetch_ms=%.1f prefetch_ms=%.1f) plates(count=%d bytes=%d compose_ms=%.1f prepare_ms=%.1f required_tiles=%d prefetched=%d) frames_s=%.2f encode_wall_s=%.2f encode_tail_s=%.2f output_bytes=%d process_wall_s=%.2f",
		cacheHit,
		summary.Frames, summary.Tile.DiskCacheHits, summary.Tile.DiskCacheMisses,
		summary.Tile.NetworkFetches, summary.Tile.Fallbacks, summary.Tile.LateFetches,
		summary.Tile.FetchMS, summary.Tile.PrefetchMS, summary.Plates.PlateCount,
		summary.Plates.PlateBytes, summary.Plates.ComposeMS, summary.Plates.PrepareMS,
		summary.Plates.RequiredTiles, summary.Plates.PrefetchFetched,
		summary.FramePipelineS, summary.RenderEncodeWallS, summary.PostFrameTailS,
		summary.OutputBytes, elapsed.Seconds())
}

// finalizeMapVideo stores the artifact in the durable cache and returns its
// identity. The cache is optional: a write failure must not discard a valid
// map artifact or turn a successful render into a failed job.
func finalizeMapVideo(cacheKey string, workspace mapRenderWorkspace, telemetryBytes []byte, cacheDirs []string) (string, int64, error) {
	sha256, size, cacheErr := storeDynamicMapVideoCacheVerified(cacheKey, workspace.output, telemetryBytes, cacheDirs...)
	if cacheErr != nil {
		log.Printf("Chronon dynamic map cache store failed: key=%s err=%v", cacheKey, cacheErr)
	}
	// The cache helper hashes while streaming bytes into its atomic cache
	// copy. When caching is disabled, or a cache write failed before source
	// bytes were consumed, hash the final output once for the import
	// attestation.
	if sha256 == "" || size <= 0 {
		hashed, hashedSize, err := hashDynamicMapVideo(workspace.output)
		if err != nil {
			return "", 0, fmt.Errorf("identify rendered dynamic map video: %w", err)
		}
		sha256, size = hashed, hashedSize
	}
	if sha256 == "" || size <= 0 {
		return "", 0, fmt.Errorf("dynamic map video identity is incomplete")
	}
	return sha256, size, nil
}

// writeMapRenderIdentity records the artifact identity beside it, so the
// import attests the exact bytes this render produced.
func writeMapRenderIdentity(workspace mapRenderWorkspace, sha256 string, size int64) error {
	identityBytes, err := json.Marshal(dynamicMapVideoIdentityManifest{SHA256: sha256, SizeBytes: size})
	if err != nil {
		return fmt.Errorf("encode dynamic map video identity: %w", err)
	}
	if err := os.WriteFile(workspace.identity, identityBytes, 0o600); err != nil {
		return fmt.Errorf("write dynamic map video identity: %w", err)
	}
	return nil
}

// publishMapDecisionReport writes the report beside the rendered MP4 (the same
// tmp workspace that becomes the content-addressed artifact dir) AND to the
// operator dir when configured — never log-only, so a wrong map is traceable
// to its job. A requested report artifact that cannot be written fails closed.
func publishMapDecisionReport(workspace mapRenderWorkspace, report *capoverlay.MapDecisionReport) error {
	if _, err := capoverlay.WriteMapDecisionReport(workspace.dir, report); err != nil {
		return fmt.Errorf("write map decision report: %w", err)
	}
	if reportDir := strings.TrimSpace(os.Getenv("PIPELINEGEN_MAP_REPORT_DIR")); reportDir != "" {
		if _, err := capoverlay.WriteMapDecisionReport(reportDir, report); err != nil {
			return fmt.Errorf("write map decision report: %w", err)
		}
	}
	return nil
}

func RenderDynamicMapVideoOnce(ctx context.Context, plan capmodel.OverlayPlan, gate DynamicMapGPUAdmission, cacheDirs ...string) (string, error) {
	profile, err := dynamicMapEncodeProfile(plan, os.Getenv(capoverlay.MapEncodeProfileEnv))
	if err != nil {
		return "", err
	}
	// The execution profile is declared up front. The native vector sampler
	// currently rasterizes on CPU, while the declared NVENC profile encodes on
	// GPU; neither stage silently substitutes its backend.
	if err := capoverlay.RequireNativeServableProfile(profile); err != nil {
		return "", err
	}
	if len(plan.Items) != 1 || plan.Items[0].Map == nil {
		return "", fmt.Errorf("expected exactly one map item")
	}
	sampler := dynamicMapSamplerForProfile(profile)
	nativeRenderer, err := nativeMapRendererPath()
	if err != nil {
		return "", err
	}
	workspace, err := newMapRenderWorkspace()
	if err != nil {
		return "", err
	}
	// Workspace ownership: every error below releases the workspace, and the
	// single success transfers it to the caller (the fold removes it after the
	// import; the prepare releases it after storing the durable cache entry).
	keepWorkspace := false
	defer func() {
		if !keepWorkspace {
			_ = os.RemoveAll(workspace.dir)
		}
	}()
	renderWidth, renderHeight := mapRenderResolution(plan)
	seed := mapRunSeed(plan)
	resolved, err := resolveMapRenderPresentation(plan, seed, mapRenderStyleSlot(plan), renderWidth, renderHeight)
	if err != nil {
		return "", err
	}
	// Composition admission covers both the requested family (an explicit
	// `ed_` request already needs the GPU compositor) and the family that was
	// actually admitted after the data gate.
	requestedComposition, _ := plan.Items[0].Params["map_animation"].(string)
	needsGPUAdmission := mapRenderRequiresGPU(profile, sampler, requestedComposition) ||
		mapRenderRequiresGPU(profile, sampler, resolved.Composition)
	// Map Decision Report: the ONE per-map record of every decision behind this
	// render — places, exclusions, composition, seed, backend, fallback and
	// phase timings — so an unexpected map is explainable without chasing logs
	// across three repositories.
	report, err := resolved.decisionReport(plan, seed)
	if err != nil {
		return "", err
	}
	log.Printf("Chronon dynamic map styles selected: seed=%s camera=%s label=%s composition=%s basemap=%s anchor=(%.4f,%.4f) zooms=[%.2f->%.2f]",
		seed, resolved.Camera, resolved.Label, resolved.Composition, resolved.Basemap, resolved.AnchorLat, resolved.AnchorLon, resolved.StartZoom, resolved.EndZoom)
	payload := resolved.payload(plan, renderWidth, renderHeight, sampler, profile)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode map render input: %w", err)
	}
	if err := os.WriteFile(workspace.input, encoded, 0600); err != nil {
		return "", fmt.Errorf("write map render input: %w", err)
	}
	rendererFingerprint, err := dynamicMapRendererFingerprint(nativeRenderer)
	if err != nil {
		return "", fmt.Errorf("identify dynamic map renderer dependencies: %w", err)
	}
	cacheKey := dynamicMapVideoCacheKey(encoded, rendererFingerprint, profile, sampler, plan.Width, plan.Height)
	renderStarted := time.Now()
	telemetryBytes, videoSHA256, videoSize, cacheHit := loadDynamicMapVideoCacheVerified(cacheKey, workspace.output, cacheDirs...)
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
		if err := executeMapRenderer(ctx, nativeRenderer, workspace); err != nil {
			return "", err
		}
		telemetryBytes, err = os.ReadFile(workspace.summary)
		if err != nil {
			return "", fmt.Errorf("read Chronon map telemetry summary: %w", err)
		}
	} else {
		log.Printf("Chronon dynamic map cache hit: key=%s", cacheKey)
		report.RecordPhase("artifact_cache_hit", 1)
		if err := os.WriteFile(workspace.summary, telemetryBytes, 0600); err != nil {
			return "", fmt.Errorf("materialize cached Chronon map telemetry: %w", err)
		}
	}
	summary, err := decodeMapRenderTelemetry(telemetryBytes, mapRenderContract{
		plan: plan, renderWidth: renderWidth, renderHeight: renderHeight, sampler: sampler, profile: profile,
	})
	if err != nil {
		return "", err
	}
	if !cacheHit && (renderWidth != plan.Width || renderHeight != plan.Height) {
		if err := scaleMapRenderVideo(ctx, workspace, plan, profile); err != nil {
			return "", err
		}
	}
	if err := validateMapRenderWork(summary); err != nil {
		return "", err
	}
	logMapRenderSummary(cacheHit, summary, time.Since(renderStarted))
	// Phase timings close the decision report.
	recordMapRenderPhases(report, summary, time.Since(renderStarted))
	// Same owner-measured phases, projected into the canonical operation
	// telemetry so the map cost is joinable to the run instead of living only
	// in the decision report and the worker log. A cache hit bypasses
	// rendering and therefore has no renderer phases to project.
	if !cacheHit {
		projectMapRendererPhases(ctx, summary.Tile.PrefetchMS, summary.Plates.PrepareMS, summary.RenderEncodeWallS)
	}
	if _, err := report.JSON(); err != nil {
		return "", fmt.Errorf("encode map decision report: %w", err)
	}
	// Renderer confirmed frames: every eligible/scheduled pin was drawn. Pins
	// without spoken timing still render as markers (no camera follow) — they
	// are rendered, with the no_spoken_timing exclusion explaining the
	// degraded camera, not a missing pin.
	for i := range report.Places {
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
	if err := publishMapDecisionReport(workspace, report); err != nil {
		return "", err
	}
	if !cacheHit {
		videoSHA256, videoSize, err = finalizeMapVideo(cacheKey, workspace, telemetryBytes, cacheDirs)
		if err != nil {
			return "", err
		}
	}
	if videoSHA256 == "" || videoSize <= 0 {
		return "", fmt.Errorf("dynamic map video identity is incomplete")
	}
	if err := writeMapRenderIdentity(workspace, videoSHA256, videoSize); err != nil {
		return "", err
	}
	keepWorkspace = true
	return workspace.output, nil
}
