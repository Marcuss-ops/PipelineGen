// Package app — minimal RenderingGen composition. This file deliberately
// owns only overlay rendering dependencies; it must not grow creator, DB,
// Qdrant, scheduler, or application-Drive dependencies.
package wiring

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/worker"
	capoverlays "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
	"go.uber.org/zap"
)

type RenderingRuntime struct {
	Registry  *worker.Registry
	Caps      appjobs.WorkerCapabilities
	Workspace *worker.Workspace
	Cache     *infraoverlays.Cache
	Log       *zap.Logger
}

func BuildRenderingRuntime(cfg *config.Config, log *zap.Logger) (*RenderingRuntime, wiring.CleanupFunc, error) {
	if cfg == nil || log == nil {
		return nil, nil, fmt.Errorf("rendering runtime: config and logger are required")
	}
	cacheRoot := os.Getenv("RENDERINGGEN_CACHE_ROOT")
	baseRoot := filepath.Join(os.TempDir(), "pipelinegen", "renderinggen")
	if cacheRoot == "" {
		cacheRoot = filepath.Join(baseRoot, "cache")
	}
	cache, err := infraoverlays.NewCache(cacheRoot)
	if err != nil {
		return nil, nil, err
	}
	workspace, err := worker.NewWorkspace(filepath.Join(baseRoot, "workspace"))
	if err != nil {
		return nil, nil, err
	}
	rendererBinary := os.Getenv("CHRONON_RENDER_BIN")
	if rendererBinary == "" {
		rendererBinary = "/opt/chronon3d/bin/chronon3d_cli"
	}
	renderer := infraoverlays.NewCommandRenderer(rendererBinary)
	lockPath := os.Getenv("RENDERINGGEN_GPU_LOCK")
	if lockPath == "" {
		lockPath = filepath.Join(os.TempDir(), "pipelinegen", "gpu-0.lock")
	}
	gate, err := infraoverlays.NewGPUGate(lockPath)
	if err != nil {
		return nil, nil, err
	}
	// The media prober certifies every rendered overlay via the canonical
	// probe port (rustexec.VideoProcessor.Probe → ffprobe) + content hash.
	// The renderer's exit code alone is never a validity criterion.
	prober := infraoverlays.NewMediaContractProber(rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log))
	handlers, err := wiring.NewHandlerSet(cache, renderer, gate, prober, os.Getenv("RENDERINGGEN_VERSION"))
	if err != nil {
		return nil, nil, err
	}
	reg := worker.NewRegistry()
	if err := reg.Register(capoverlays.JobTypePrepare, handlers.Prepare); err != nil {
		return nil, nil, err
	}
	if err := reg.Register(capoverlays.JobTypeRender, handlers.Render); err != nil {
		return nil, nil, err
	}
	reg.SetProducesArtifacts(capoverlays.JobTypeRender, true).Freeze()
	caps := appjobs.WorkerCapabilities{JobTypes: reg.JobTypes(), GPU: true, FFmpeg: true}
	cleanup := func() { _ = os.RemoveAll(workspace.Root) }
	return &RenderingRuntime{Registry: reg, Caps: caps, Workspace: workspace, Cache: cache, Log: log}, cleanup, nil
}
