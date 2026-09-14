// Package app — minimal RenderingGen composition. This file deliberately
// owns only overlay rendering dependencies; it must not grow creator, DB,
// Qdrant, scheduler, or application-Drive dependencies.
package wiring

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/worker"
	capoverlays "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
	"go.uber.org/zap"
)

// DefaultGPUGateSlots is the conservative overlay GPU concurrency: one
// exclusive flock, i.e. the historical host-wide serialization.
//
// Raising it is only defensible together with the GPU peers:
//
//   - RenderingGen's worker `gpu_lanes` is the measured ceiling (2 on the
//     reference host — see RenderingGen/renderinggen/config.yaml and
//     RenderingGen/infra/native/renderinggen-native.yaml, which records that
//     extra lanes add queue wait, VRAM pressure and text-path lock contention
//     without increasing the render-loop rate);
//   - a Chronon video job is itself mutex-serialized by the daemon
//     execution-domain contract
//     (Chronon3d/apps/chronon3d_cli/daemon/daemon_render_concurrency.hpp).
//
// Every process sharing the GPU must therefore be given the SAME
// RENDERINGGEN_GPU_SLOTS value (see overlays.GPUGate).
const DefaultGPUGateSlots = 1

// resolveGPUGateSlots parses RENDERINGGEN_GPU_SLOTS. `explicit` reports whether
// an operator actually configured a value, so the caller can distinguish "the
// default applied" from "the operator chose one slot"; an unparsable or
// out-of-range value falls back to the default and is reported as not explicit.
func resolveGPUGateSlots(raw string) (slots int, explicit bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return DefaultGPUGateSlots, false
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 1 {
		return DefaultGPUGateSlots, false
	}
	return n, true
}

// gpuGateSlots reads the overlay GPU slot count from the environment.
func gpuGateSlots() (slots int, explicit bool) {
	return resolveGPUGateSlots(os.Getenv("RENDERINGGEN_GPU_SLOTS"))
}

type RenderingRuntime struct {
	Registry  *worker.Registry
	Caps      appjobs.WorkerCapabilities
	Workspace *worker.Workspace
	Cache     *infraoverlays.Cache
	Log       *zap.Logger
}

func BuildRenderingRuntime(cfg *config.Config, log *zap.Logger) (*RenderingRuntime, CleanupFunc, error) {
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
	slots, explicitSlots := gpuGateSlots()
	gate, err := infraoverlays.NewGPUGateWithSlots(lockPath, slots)
	if err != nil {
		return nil, nil, err
	}
	if explicitSlots {
		log.Info("overlay GPU gate slots resolved",
			zap.Int("slots", slots), zap.String("gpu_lock", lockPath))
	} else {
		// Not a failure: the default is the historical serialization. It must be
		// visible, though, because a peer process given a different value does not
		// share this GPU admission contract.
		log.Warn("RENDERINGGEN_GPU_SLOTS is unset: overlay renders serialize on one GPU slot; set it to RenderingGen's worker.gpu_lanes so every process sharing the GPU uses the same admission contract",
			zap.Int("slots", slots), zap.String("gpu_lock", lockPath))
	}
	// The media prober certifies every rendered overlay via the canonical
	// probe port (rustexec.VideoProcessor.Probe → ffprobe) + content hash.
	// The renderer's exit code alone is never a validity criterion.
	prober := infraoverlays.NewMediaContractProber(rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log))
	handlers, err := NewHandlerSet(cache, renderer, gate, prober, os.Getenv("RENDERINGGEN_VERSION"))
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
