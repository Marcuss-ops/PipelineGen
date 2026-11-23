// Package app — minimal RenderingGen composition. This file deliberately
// owns only overlay rendering dependencies; it must not grow creator, DB,
// pgvector, scheduler, or application-Drive dependencies.
package rendering

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/worker"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
	"go.uber.org/zap"
)

// DefaultGPUGateSlots mirrors RenderingGen's shipped worker lane count.
//
// 2 is the certified value after the 2026-11-22 VRAM incident: three
// concurrent GPU sessions plus resident Ollama reproduced
// VK_ERROR_OUT_OF_DEVICE_MEMORY, while two lanes completed the same job with
// 15.4 GB of 16.3 GB free. The shipped config (`gpu_lanes: 2`) and the systemd
// drop-in (`RENDERINGGEN_GPU_SLOTS=2`) are the authority; this constant is the
// built-in fallback and must never admit more work than the worker can run.
//
// Every process sharing the GPU must be given the SAME
// RENDERINGGEN_GPU_SLOTS value (see overlays.GPUGate).
const DefaultGPUGateSlots = infraoverlays.DefaultGPUGateSlots

func newOverlayGPUGate() (*infraoverlays.GPUGate, string, int, error) {
	return infraoverlays.NewConfiguredGPUGate()
}

// LocalOverlayRenderEnabled gate il percorso renderer locale GPU (P3-5, Fase 6).
// Default true: PipelineGen resta produttore+orchestratore+consumatore con
// fallback locale finché la migrazione dei consumer clip (durable resolver)
// non è verificata. Il cutover imposta PIPELINEGEN_LOCAL_RENDER=0: a quel
// punto il registry locale non registra più overlay.render e i render
// passano solo da queue/worker RenderingGen. Non toccare clip.render finale.
func LocalOverlayRenderEnabled() bool {
	raw := os.Getenv("PIPELINEGEN_LOCAL_RENDER")
	if raw == "" {
		return true
	}
	return raw == "1"
}

type RenderingRuntime struct {
	Registry  *worker.Registry
	Caps      appjobs.WorkerCapabilities
	Workspace *worker.Workspace
	Cache     *infraoverlays.Cache
	Log       *zap.Logger
}

func BuildRenderingRuntime(cfg *config.Config, log *zap.Logger) (*RenderingRuntime, func(), error) {
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
	gate, lockPath, slots, err := newOverlayGPUGate()
	explicitSlots := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_SLOTS")) != ""
	if err != nil {
		return nil, nil, err
	}
	if rawLanes := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_LANES")); rawLanes != "" {
		lanes, laneErr := strconv.Atoi(rawLanes)
		if laneErr != nil {
			return nil, nil, fmt.Errorf("RENDERINGGEN_GPU_LANES=%q is not an integer: %w", rawLanes, laneErr)
		}
		if err := infraoverlays.ValidateGPUSlotParity(lanes, slots); err != nil {
			return nil, nil, err
		}
		log.Info("GPU admission parity verified",
			zap.Int("worker_gpu_lanes", lanes), zap.Int("admission_slots", slots))
	}
	if explicitSlots {
		log.Info("overlay GPU gate slots resolved",
			zap.Int("slots", slots), zap.String("gpu_lock", lockPath))
	} else {
		// Not a failure: the built-in default (DefaultGPUGateSlots) is the
		// measured overlay concurrency. It must be visible, though, because a
		// peer process given a different value does not share this GPU admission
		// contract.
		log.Warn("RENDERINGGEN_GPU_SLOTS is unset: using the built-in default admission; set it explicitly to RenderingGen's worker.gpu_lanes so every process sharing the GPU declares the same contract",
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
	if err := reg.Register(capmodel.JobTypePrepare, handlers.Prepare); err != nil {
		return nil, nil, err
	}
	// P3-5: il render locale si disattiva solo via gate esplicito dopo
	// verifica consumer (durable resolver). Default ancora abilitato.
	if LocalOverlayRenderEnabled() {
		if err := reg.Register(capmodel.JobTypeRender, handlers.Render); err != nil {
			return nil, nil, err
		}
		reg.SetProducesArtifacts(capmodel.JobTypeRender, true).Freeze()
	} else {
		log.Info("local overlay.render disabled: renders go through RenderingGen queue/worker only")
		reg.Freeze()
	}
	caps := appjobs.WorkerCapabilities{JobTypes: reg.JobTypes(), GPU: true, FFmpeg: true}
	cleanup := func() { _ = os.RemoveAll(workspace.Root) }
	return &RenderingRuntime{Registry: reg, Caps: caps, Workspace: workspace, Cache: cache, Log: log}, cleanup, nil
}
