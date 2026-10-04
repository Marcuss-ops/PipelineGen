package wiring

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

	finalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/geodesy"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

type HandlerSet struct {
	Cache           *overlays.Cache
	AssetPreparer   *overlays.AssetPreparer
	Renderer        overlays.Renderer
	GPUGate         *overlays.GPUGate
	Prober          capoverlay.MediaProber
	RendererVersion string
}

func NewHandlerSet(cache *overlays.Cache, renderer overlays.Renderer, gate *overlays.GPUGate, prober capoverlay.MediaProber, version string) (*HandlerSet, error) {
	if cache == nil || renderer == nil || gate == nil || prober == nil {
		return nil, fmt.Errorf("overlay handlers: cache, renderer, gpu gate and media prober are required")
	}
	if version == "" {
		version = "renderinggen-dev"
	}
	preparer, err := overlays.NewAssetPreparer(cache)
	if err != nil {
		return nil, err
	}
	return &HandlerSet{Cache: cache, AssetPreparer: preparer, Renderer: renderer, GPUGate: gate, Prober: prober, RendererVersion: version}, nil
}

func (h *HandlerSet) Prepare(ctx context.Context, j *job.Job, _ *job.JobExecutionTools) (map[string]any, error) {
	var req capoverlay.PrepareRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("overlay.prepare payload: %w", err)
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	// Pre-timing prepare: prefetch the entity-image assets referenced by the
	// OverlayIntents so the later timing-frozen overlay.render finds them
	// warm. Template resolution is PipelineGen-owned (the template_id is
	// already bound on each intent); this worker only warms the assets.
	// The asset warm is the RenderingGen "materialize" phase, so it is
	// mapped into the canonical run model as one operation — never a new
	// timing family.
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageProcess,
		Component: kernobs.ComponentRenderingGen,
		Operation: kernobs.OperationMaterialize,
		Items:     int64(len(req.Intents)),
	}, func(ctx context.Context) error {
		assets := make([]overlays.AssetRef, 0)
		for _, intent := range req.Intents {
			for _, ref := range intent.Payload.AssetRefs {
				assets = append(assets, overlays.AssetRef{AssetID: ref.AssetID, URL: ref.URL, SHA256: ref.SHA256})
			}
		}
		_, err := h.AssetPreparer.Prepare(ctx, assets)
		return err
	}); err != nil {
		return nil, err
	}
	return map[string]any{
		"schema_version": capoverlay.SchemaVersionPrepare,
		"plan_id":        req.PlanID,
		"prepared":       len(req.Intents),
	}, nil
}

func (h *HandlerSet) Render(ctx context.Context, j *job.Job, _ *job.JobExecutionTools) (map[string]any, error) {
	var req capoverlay.RenderRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("overlay.render payload: %w", err)
	}
	// plan: the render plan is compiled, validated and the media contract
	// resolved before any pixel work. Mapped as the RenderingGen "plan"
	// phase operation on the canonical run.
	var (
		item     *capoverlay.OverlayItem
		contract capoverlay.OverlayMediaContract
	)
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageProcess,
		Component: kernobs.ComponentRenderingGen,
		Operation: kernobs.OperationPlan,
	}, func(ctx context.Context) error {
		if req.Plan.RendererVersion == "" {
			req.Plan.RendererVersion = h.RendererVersion
		}
		if err := req.Plan.Validate(); err != nil {
			return err
		}
		for i := range req.Plan.Items {
			if req.Plan.Items[i].ID == req.OverlayID {
				item = &req.Plan.Items[i]
				break
			}
		}
		if item == nil {
			return fmt.Errorf("overlay.render: overlay_id %q not found", req.OverlayID)
		}
		if item.RenderKey == "" {
			item.RenderKey = capoverlay.ComputeRenderKey(req.Plan, *item)
		}
		// The render container/codec/alpha come from the plan's media
		// contract — never a hardcoded .mov guess. The contract is the
		// single owner of the output format; the worker only materializes
		// it.
		c, err := capoverlay.ResolveMediaContract(req.Plan.MediaContract)
		if err != nil {
			return err
		}
		contract = c
		return nil
	}); err != nil {
		return nil, err
	}
	// materialize: every asset referenced by the overlay is resolved into
	// the cache before rendering (the RenderingGen "materialize" phase).
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageProcess,
		Component: kernobs.ComponentRenderingGen,
		Operation: kernobs.OperationMaterialize,
		Items:     int64(len(item.AssetRefs)),
	}, func(ctx context.Context) error {

		assets := make([]overlays.AssetRef, 0, len(item.AssetRefs))
		for _, ref := range item.AssetRefs {
			assets = append(assets, overlays.AssetRef{AssetID: ref.AssetID, URL: ref.URL, SHA256: ref.SHA256})
		}
		_, err := h.AssetPreparer.Prepare(ctx, assets)
		return err
	}); err != nil {
		return nil, err
	}
	container := contract.Container
	if container == "" {
		container = "mp4"
	}
	// Must match worker.Workspace.JobDir: the runner owns cleanup after the
	// manifest has been uploaded. RenderingGen never leaves job state in its
	// disposable cache.
	jobDir := filepath.Join(os.TempDir(), "pipelinegen", "renderinggen", "workspace", "jobs", j.ID, "output")
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return nil, err
	}
	output := filepath.Join(jobDir, safeName(item.ID)+"."+container)
	// Every overlay render is a fresh Chronon execution. The content cache is
	// intentionally used only by AssetPreparer for reusable input resources
	// (entity images, backgrounds and fonts); a previous rendered overlay is
	// never a valid substitute for the new job's output.
	//
	// This is deliberately different from an ordinary content-addressed media
	// cache: two runs may have identical semantic inputs, but the caller still
	// requires a newly rendered and newly published overlay artifact.
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageProcess,
		Component: kernobs.ComponentRenderingGen,
		Operation: kernobs.OperationRender,
	}, func(ctx context.Context) error {
		release, err := h.GPUGate.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("overlay.render: acquire GPU gate: %w", err)
		}
		defer release()
		planJSON, err := json.Marshal(req.Plan)
		if err != nil {
			return err
		}
		if err := h.Renderer.Render(ctx, planJSON, output); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}
	// No rendered-output cache write: the previous write-through branch was
	// dead weight — nothing ever read it back (render is deliberately fresh
	// every time) and it copied the full video on every job. The certified
	// artifact is published through the artifact manifest / Drive publisher
	// below, which is the sole durable output path.
	// The rendered artifact is certified only after a canonical probe (ffprobe
	// via rustexec.VideoProcessor.Probe, never a raw subprocess) + content
	// hash. The renderer's exit code is NOT a validity criterion: an invalid
	// or stub render that still exited 0 fails closed here. The probe+hash
	// call is the RenderingGen "sha256" phase, mapped as one canonical
	// operation.
	var probed capoverlay.OverlayProbeResult
	if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageProcess,
		Component: kernobs.ComponentRenderingGen,
		Operation: kernobs.OperationHash,
	}, func(ctx context.Context) error {
		p, err := h.Prober.ProbeOverlay(ctx, output)
		if err != nil {
			return err
		}
		probed = p
		return nil
	}); err != nil {
		return nil, err
	}
	if err := contract.Validate(probed); err != nil {
		return nil, fmt.Errorf("overlay.render media contract: %w", err)
	}
	mime := "video/mp4"
	if container == "mov" {
		mime = "video/quicktime"
	}
	// Canonical integer-microsecond timing drives the artifact duration;
	// the millisecond projection is only a reporting convenience.
	durationUS := item.EndUSValue() - item.StartUSValue()
	// The result is stamped READY only here — after render + probe + contract
	// validation + hash have all succeeded. The probed facts travel with the
	// result so the Sender can persist them durably.
	result := capoverlay.RenderResult{SchemaVersion: capoverlay.SchemaVersionResult, OverlayID: item.ID, PlanID: req.Plan.PlanID, PlanFingerprint: req.Plan.Fingerprint, RenderKey: item.RenderKey, ArtifactID: j.ID + ":" + item.ID, Filename: safeName(j.ID) + "_" + safeName(item.ID) + "." + container, LocalPath: output, SHA256: probed.SHA256, SizeBytes: probed.SizeBytes, MIMEType: mime, Width: probed.Width, Height: probed.Height, FPSNum: req.Plan.FPSNum, FPSDen: req.Plan.FPSDen, DurationMs: (durationUS + 999) / 1000, HasAlpha: contract.RequiresAlpha, RendererVersion: h.RendererVersion, SceneID: item.SceneID, TemplateID: item.TemplateID, MediaContract: contract.ID, Container: probed.Container, Codec: probed.Codec, PixelFormat: probed.PixelFormat, AudioStreams: probed.AudioStreams, Status: capoverlay.OverlayStatusReady}
	return artifactResult(j.ID, req.Plan.VideoID, req.Plan.ProjectID, req.Plan.ScriptName, req.Plan.Language, result)
}

func artifactResult(jobID, videoID, projectID, scriptName, language string, result capoverlay.RenderResult) (map[string]any, error) {
	driveSubpath := []string{finalization.OverlayChildFolder}
	manifest := job.ArtifactManifest{SchemaVersion: job.SchemaVersionArtifactManifestV1, JobID: jobID, Artifacts: []job.Artifact{{
		ID:        result.ArtifactID,
		Kind:      job.ArtifactKindOverlay,
		Path:      result.LocalPath,
		Filename:  result.Filename,
		MIMEType:  result.MIMEType,
		SizeBytes: result.SizeBytes,
		SHA256:    result.SHA256,
		Required:  true,
		// SHA256 and Drive routing live ON the ArtifactManifest, never in a
		// parallel pipeline: the probe (SHA-256 + size) is folded into the
		// manifest so the Sender-side ArtifactPreparation + Drive publisher
		// consume a single source of truth and persist location + sha256.
		ArtifactMetadata: map[string]any{
			"source":           "chronon",
			"drive_subpath":    driveSubpath,
			"video_id":         videoID,
			"project_id":       projectID,
			"renderer_version": result.RendererVersion,
			"duration_us":      result.DurationMs * 1000,
			"duration_ms":      result.DurationMs,
			"plan_fingerprint": result.PlanFingerprint,
			"render_key":       result.RenderKey,
			"overlay_id":       result.OverlayID,
			// Probed media-contract facts + render-worker certification. These
			// are the durable record that the artifact was contract-validated
			// and hashed before publication; the Sender fills drive_file_id /
			// drive_link (on the Artifact struct) after the Drive publisher
			// runs, completing the READY gate (rendered + probed +
			// contract-valid + hashed + uploaded + persisted).
			"media_contract": result.MediaContract,
			"container":      result.Container,
			"codec":          result.Codec,
			"pixel_format":   result.PixelFormat,
			"audio_streams":  result.AudioStreams,
			"scene_id":       result.SceneID,
			"template_id":    result.TemplateID,
			"status":         result.Status,
		},
	}}}
	if strings.TrimSpace(scriptName) != "" {
		manifest.Artifacts[0].ArtifactMetadata["script_name"] = scriptName
	}
	if strings.TrimSpace(language) != "" {
		manifest.Artifacts[0].ArtifactMetadata["language"] = language
	}
	return map[string]any{"schema_version": capoverlay.SchemaVersionResult, "overlay_result": result, job.ManifestKey: manifest}, nil
}

func safeName(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "overlay"
	}
	var b strings.Builder
	for _, r := range v {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// ── Chronon template map plate resolver (merged from
// chronon_template_map_plates.go: same overlay-wiring domain, and the merge
// keeps the wiring package at its registered hotspot file baseline while the
// digest identity migrates to the kernel SSOT) ──────────────────────────────

const (
	chrononMapLicense     = "Esri World Imagery service; terms: https://goto.arcgisonline.com/maps/World_Imagery"
	chrononMapAttribution = "Source: Esri, Vantor, Earthstar Geographics, and the GIS User Community"
)

// chrononTemplateMapPlateResolver adapts ChrononTemplate's georeferenced tile
// sampler to PipelineGen's static-plate contract. It renders each requested
// zoom once into a durable local cache; the ordinary map compiler then checks
// the raster window, draws pins/labels/attribution, and renders the camera move.
type chrononTemplateMapPlateResolver struct {
	generator string
	cacheDir  string
	mu        sync.Mutex
}

func newChrononTemplateMapPlateResolver(generator, cacheDir string) *chrononTemplateMapPlateResolver {
	return &chrononTemplateMapPlateResolver{
		generator: strings.TrimSpace(generator),
		cacheDir:  strings.TrimSpace(cacheDir),
	}
}

func (r *chrononTemplateMapPlateResolver) ResolvePlate(latitude, longitude float64) (capoverlay.MapPlate, bool) {
	plate, ok := r.ResolveFlyover(
		capoverlay.MapCenter{Latitude: latitude, Longitude: longitude},
		capoverlay.MapCenter{Latitude: latitude, Longitude: longitude},
		1920, 1080,
	)
	return plate, ok
}

func (r *chrononTemplateMapPlateResolver) ResolveFlyover(from, to capoverlay.MapCenter, width, height int) (capoverlay.MapPlate, bool) {
	if r == nil || r.generator == "" || r.cacheDir == "" || width <= 0 || height <= 0 {
		return capoverlay.MapPlate{}, false
	}
	if (geodesy.Point{Latitude: from.Latitude, Longitude: from.Longitude}).Validate() != nil ||
		(geodesy.Point{Latitude: to.Latitude, Longitude: to.Longitude}).Validate() != nil {
		return capoverlay.MapPlate{}, false
	}
	// Camera LODs need bleed around the output viewport during cross-fades
	// AND around the route's own extent: a multi-city run merges its mentions
	// into ONE route whose endpoints (and intermediate pins) can sit far from
	// the plate centre, so the projected window is 3x the canvas and the
	// raster is that same resolution (the previous 2x logical / 1x raster
	// split made the LOD coverage contract impossible to satisfy and
	// distorted the georeference).
	plateWidth := width * 3
	plateHeight := height * 3
	center := routeMapCenter(from, to)
	// Adjacent zoom levels keep each raster active only across a narrow
	// cross-fade interval, so its geographic window can contain the complete
	// camera viewport throughout that interval.
	// Start at the regional view and finish one zoom level closer. A long
	// zoom ladder made a single city collapse into an endless push-in instead
	// of keeping the route readable while the camera travels between places.
	zooms := []int{6, 7}
	covered := make([]int, 0, len(zooms))
	for _, zoom := range zooms {
		window := geodesy.CenteredOn(center.Latitude, center.Longitude, zoom, plateWidth, plateHeight)
		if window.Contains(from.Latitude, from.Longitude) && window.Contains(to.Latitude, to.Longitude) {
			covered = append(covered, zoom)
		}
	}
	// The worker validates every LOD over the exact zoom interval where its
	// opacity is > 0 and fails the whole compile when one misses the camera
	// viewport. Apply that same bound HERE and drop the finest zooms until
	// the surviving ladder passes, so a long route degrades to fewer LODs
	// instead of voiding the plan.
	covered = trimZoomsToViewport(covered, plateWidth, plateHeight, center, from, to)
	if len(covered) == 0 {
		log.Printf("chronontemplate map resolver: no zoom covers route from=(%.6f,%.6f) to=(%.6f,%.6f) size=%dx%d", from.Latitude, from.Longitude, to.Latitude, to.Longitude, width, height)
		return capoverlay.MapPlate{}, false
	}

	plates := make([]capoverlay.MapPlate, 0, len(covered))
	for _, zoom := range covered {
		plate, ok := r.generate(center, zoom, plateWidth, plateHeight)
		if !ok {
			log.Printf("chronontemplate map resolver: plate generation failed center=(%.6f,%.6f) zoom=%d size=%dx%d", center.Latitude, center.Longitude, zoom, width, height)
			return capoverlay.MapPlate{}, false
		}
		plates = append(plates, plate)
	}
	base := plates[0]
	if len(plates) > 1 {
		base.LODs = append([]capoverlay.MapPlate(nil), plates[1:]...)
	}
	return base, true
}

// mapPlateFadeHalfBandZoom mirrors the worker's mapLODFadeHalfBandZoom: the
// half-width in zoom stops of every LOD cross-fade.
const mapPlateFadeHalfBandZoom = 0.5

// trimZoomsToViewport drops the finest zooms from the ladder until every
// surviving LOD covers the camera viewport across its active interval (the
// worker's mapLODWindowCoversMove contract, mirrored here). Returns nil when
// fewer than two zooms survive — the caller fails closed.
func trimZoomsToViewport(covered []int, plateWidth, plateHeight int, center, from, to capoverlay.MapCenter) []int {
	for len(covered) >= 2 {
		startZoom := float64(covered[0])
		endZoom := float64(covered[len(covered)-1])
		ok := true
		for index, zoom := range covered {
			low, high := float64(zoom), float64(zoom)
			if index == 0 {
				low = startZoom
			} else {
				low = (float64(covered[index-1])+float64(zoom))/2 - mapPlateFadeHalfBandZoom
				if low < startZoom {
					low = startZoom
				}
			}
			if index == len(covered)-1 {
				high = endZoom
			} else {
				high = (float64(zoom)+float64(covered[index+1]))/2 + mapPlateFadeHalfBandZoom
				if high > endZoom {
					high = endZoom
				}
			}
			window := geodesy.CenteredOn(center.Latitude, center.Longitude, zoom, plateWidth, plateHeight)
			if !window.CoversMove(
				geodesy.Point{Latitude: from.Latitude, Longitude: from.Longitude},
				geodesy.Point{Latitude: to.Latitude, Longitude: to.Longitude},
				startZoom, endZoom, low, high, plateWidth/3, plateHeight/3) {
				ok = false
				break
			}
		}
		if ok {
			return covered
		}
		covered = covered[:len(covered)-1]
	}
	return nil
}

func routeMapCenter(from, to capoverlay.MapCenter) capoverlay.MapCenter {
	lonTo := to.Longitude
	delta := lonTo - from.Longitude
	if delta > 180 {
		lonTo -= 360
	} else if delta < -180 {
		lonTo += 360
	}
	lon := (from.Longitude + lonTo) / 2
	for lon > 180 {
		lon -= 360
	}
	for lon < -180 {
		lon += 360
	}
	return capoverlay.MapCenter{Latitude: (from.Latitude + to.Latitude) / 2, Longitude: lon}
}

func (r *chrononTemplateMapPlateResolver) generate(center capoverlay.MapCenter, zoom, width, height int) (capoverlay.MapPlate, bool) {
	// Serialize identical jobs so concurrent script runs cannot write the same
	// cache entry while the Python renderer is creating it.
	r.mu.Lock()
	defer r.mu.Unlock()
	// The raster is the declared window; keep the legacy 2x-split keys out of
	// the same cache namespace so stale stretched plates cannot be reused.
	key := fmt.Sprintf("%.6f_%.6f_z%d_%dx%d", center.Latitude, center.Longitude, zoom, width, height)
	key = strings.NewReplacer("-", "m", ".", "p").Replace(key)
	path := filepath.Join(r.cacheDir, "esri-world-imagery-v2", key+".png")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return capoverlay.MapPlate{}, false
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		// The service prepends its Whisper venv to PATH. That venv has
		// faster-whisper but not OpenCV, while the system Python has cv2 (a
		// required dependency of ChrononTemplate's tile compositor). Resolve
		// this helper with the system interpreter so runtime PATH cannot select
		// an incompatible environment.
		cmd := exec.CommandContext(ctx, "/usr/bin/python3", r.generator,
			"--latitude", fmt.Sprintf("%.8f", center.Latitude),
			"--longitude", fmt.Sprintf("%.8f", center.Longitude),
			"--zoom", fmt.Sprint(zoom),
			"--width", fmt.Sprint(width), "--height", fmt.Sprint(height),
			"--output", path,
		)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("chronontemplate map resolver: generator failed zoom=%d: %v: %s", zoom, err, strings.TrimSpace(string(output)))
			_ = os.Remove(path)
			return capoverlay.MapPlate{}, false
		} else if !strings.Contains(string(output), "MAP_PLATE_PASS") {
			log.Printf("chronontemplate map resolver: generator did not certify zoom=%d: %s", zoom, strings.TrimSpace(string(output)))
			_ = os.Remove(path)
			return capoverlay.MapPlate{}, false
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return capoverlay.MapPlate{}, false
	}
	// godlike/06 digest SSOT: content identity goes through kernel/digest,
	// never a direct crypto/sha256 import.
	assetDigest := digest.SHA256Bytes(raw)
	id := fmt.Sprintf("chronontemplate-esri-%s-z%d", key, zoom)
	return capoverlay.MapPlate{
		ID: id, License: chrononMapLicense, Attribution: chrononMapAttribution,
		Center: center, Zoom: zoom, Width: width, Height: height,
		Window: geodesy.CenteredOn(center.Latitude, center.Longitude, zoom, width, height),
		Asset: capoverlay.NewOverlayAssetRef(
			asset.New(id, assetDigest, "image/png", 0), "", path,
		),
	}, true
}

var _ capoverlay.PlateResolver = (*chrononTemplateMapPlateResolver)(nil)
var _ capoverlay.FlyoverPlateResolver = (*chrononTemplateMapPlateResolver)(nil)
