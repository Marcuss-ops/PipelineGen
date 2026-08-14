package overlays

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
)

type HandlerSet struct {
	Cache           *overlays.Cache
	Renderer        overlays.Renderer
	GPUGate         *overlays.GPUGate
	RendererVersion string
}

func NewHandlerSet(cache *overlays.Cache, renderer overlays.Renderer, gate *overlays.GPUGate, version string) (*HandlerSet, error) {
	if cache == nil || renderer == nil || gate == nil {
		return nil, fmt.Errorf("overlay handlers: cache, renderer and gpu gate are required")
	}
	if version == "" {
		version = "renderinggen-dev"
	}
	return &HandlerSet{Cache: cache, Renderer: renderer, GPUGate: gate, RendererVersion: version}, nil
}

func (h *HandlerSet) Prepare(ctx context.Context, j *job.Job, _ *job.JobExecutionTools) (map[string]any, error) {
	var plan capoverlay.OverlayPlan
	if err := json.Unmarshal(j.Payload, &plan); err != nil {
		return nil, fmt.Errorf("overlay.prepare payload: %w", err)
	}
	if plan.RendererVersion == "" {
		plan.RendererVersion = h.RendererVersion
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	for _, item := range plan.Items {
		for _, ref := range item.AssetRefs {
			if _, err := h.Cache.EnsureAsset(ctx, ref.URL, ref.SHA256); err != nil {
				return nil, fmt.Errorf("overlay.prepare asset %s: %w", ref.AssetID, err)
			}
		}
	}
	hits := 0
	for _, item := range plan.Items {
		if h.Cache.Has("overlays", item.RenderKey, "overlay.mov") {
			hits++
		}
	}
	return map[string]any{"schema_version": capoverlay.SchemaVersionResult, "plan_id": plan.PlanID, "plan_fingerprint": plan.Fingerprint, "prepared": len(plan.Items), "cache_hits": hits}, nil
}

func (h *HandlerSet) Render(ctx context.Context, j *job.Job, _ *job.JobExecutionTools) (map[string]any, error) {
	var req capoverlay.RenderRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("overlay.render payload: %w", err)
	}
	if req.Plan.RendererVersion == "" {
		req.Plan.RendererVersion = h.RendererVersion
	}
	if err := req.Plan.Validate(); err != nil {
		return nil, err
	}
	var item *capoverlay.OverlayItem
	for i := range req.Plan.Items {
		if req.Plan.Items[i].ID == req.OverlayID {
			item = &req.Plan.Items[i]
			break
		}
	}
	if item == nil {
		return nil, fmt.Errorf("overlay.render: overlay_id %q not found", req.OverlayID)
	}
	if item.RenderKey == "" {
		item.RenderKey = capoverlay.ComputeRenderKey(req.Plan, *item)
	}
	for _, ref := range item.AssetRefs {
		if _, err := h.Cache.EnsureAsset(ctx, ref.URL, ref.SHA256); err != nil {
			return nil, fmt.Errorf("overlay.render asset %s: %w", ref.AssetID, err)
		}
	}
	// Must match worker.Workspace.JobDir: the runner owns cleanup after the
	// manifest has been uploaded. RenderingGen never leaves job state in its
	// disposable cache.
	jobDir := filepath.Join(os.TempDir(), "pipelinegen", "renderinggen", "workspace", "jobs", j.ID, "output")
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return nil, err
	}
	output := filepath.Join(jobDir, safeName(item.ID)+".mov")
	if h.Cache.Has("overlays", item.RenderKey, "overlay.mov") {
		cached := h.Cache.Path("overlays", item.RenderKey, "overlay.mov")
		if err := copyFile(cached, output); err != nil {
			return nil, err
		}
	} else {
		release, err := h.GPUGate.Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("overlay.render: acquire GPU gate: %w", err)
		}
		planJSON, err := json.Marshal(req.Plan)
		if err != nil {
			release()
			return nil, err
		}
		if err := h.Renderer.Render(ctx, planJSON, output); err != nil {
			release()
			return nil, err
		}
		release()
		if _, err := h.Cache.PutFile("overlays", item.RenderKey, "overlay.mov", output); err != nil {
			return nil, err
		}
	}
	sha, err := fileSHA(output)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(output)
	if err != nil {
		return nil, err
	}
	result := capoverlay.RenderResult{SchemaVersion: capoverlay.SchemaVersionResult, OverlayID: item.ID, PlanID: req.Plan.PlanID, PlanFingerprint: req.Plan.Fingerprint, RenderKey: item.RenderKey, ArtifactID: j.ID + ":" + item.ID, Filename: safeName(item.ID) + ".mov", LocalPath: output, SHA256: sha, SizeBytes: info.Size(), MIMEType: "video/quicktime", Width: req.Plan.Width, Height: req.Plan.Height, FPS: req.Plan.FPS, DurationMs: item.EndMs - item.StartMs, HasAlpha: true, RendererVersion: h.RendererVersion}
	return artifactResult(j.ID, result)
}

func artifactResult(jobID string, result capoverlay.RenderResult) (map[string]any, error) {
	manifest := job.ArtifactManifest{SchemaVersion: job.SchemaVersionArtifactManifestV1, JobID: jobID, Artifacts: []job.Artifact{{ID: result.ArtifactID, Kind: job.ArtifactKindOverlay, Path: result.LocalPath, Filename: result.Filename, MIMEType: result.MIMEType, SizeBytes: result.SizeBytes, SHA256: result.SHA256, Required: true, ArtifactMetadata: map[string]any{"source": "overlay", "drive_subpath": []string{"overlay"}, "plan_fingerprint": result.PlanFingerprint, "render_key": result.RenderKey, "overlay_id": result.OverlayID}}}}
	return map[string]any{"schema_version": capoverlay.SchemaVersionResult, "overlay_result": result, job.ManifestKey: manifest}, nil
}

func fileSHA(path string) (string, error) {
	return overlays.SHA256File(path)
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
