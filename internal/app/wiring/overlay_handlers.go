package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
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
	if h.Cache.Has("overlays", item.RenderKey, "overlay."+container) {
		cached := h.Cache.Path("overlays", item.RenderKey, "overlay."+container)
		if err := copyFile(cached, output); err != nil {
			return nil, err
		}
	} else {
		// render: the GPU-gated Chronon render call — the RenderingGen
		// "render" phase, mapped as one canonical operation.
		if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     kernobs.StageProcess,
			Component: kernobs.ComponentRenderingGen,
			Operation: kernobs.OperationRender,
		}, func(ctx context.Context) error {
			release, err := h.GPUGate.Acquire(ctx)
			if err != nil {
				return fmt.Errorf("overlay.render: acquire GPU gate: %w", err)
			}
			planJSON, err := json.Marshal(req.Plan)
			if err != nil {
				release()
				return err
			}
			if err := h.Renderer.Render(ctx, planJSON, output); err != nil {
				release()
				return err
			}
			release()
			return nil
		}); err != nil {
			return nil, err
		}
		// objectstore_upload: persisting the rendered bytes into the content
		// cache — the RenderingGen "objectstore_upload" phase.
		if err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
			Stage:     kernobs.StageProcess,
			Component: kernobs.ComponentRenderingGen,
			Operation: kernobs.OperationObjectStoreUpload,
		}, func(ctx context.Context) error {
			if _, err := h.Cache.PutFile("overlays", item.RenderKey, "overlay."+container, output); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
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
	result := capoverlay.RenderResult{SchemaVersion: capoverlay.SchemaVersionResult, OverlayID: item.ID, PlanID: req.Plan.PlanID, PlanFingerprint: req.Plan.Fingerprint, RenderKey: item.RenderKey, ArtifactID: j.ID + ":" + item.ID, Filename: safeName(item.ID) + "." + container, LocalPath: output, SHA256: probed.SHA256, SizeBytes: probed.SizeBytes, MIMEType: mime, Width: probed.Width, Height: probed.Height, FPSNum: req.Plan.FPSNum, FPSDen: req.Plan.FPSDen, DurationMs: (durationUS + 999) / 1000, HasAlpha: contract.RequiresAlpha, RendererVersion: h.RendererVersion, SceneID: item.SceneID, TemplateID: item.TemplateID, MediaContract: contract.ID, Container: probed.Container, Codec: probed.Codec, PixelFormat: probed.PixelFormat, AudioStreams: probed.AudioStreams, Status: capoverlay.OverlayStatusReady}
	return artifactResult(j.ID, req.Plan.VideoID, req.Plan.ProjectID, result)
}

func artifactResult(jobID, videoID, projectID string, result capoverlay.RenderResult) (map[string]any, error) {
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
			"drive_subpath":    []string{"overlay"},
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
	return map[string]any{"schema_version": capoverlay.SchemaVersionResult, "overlay_result": result, job.ManifestKey: manifest}, nil
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0644)
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
