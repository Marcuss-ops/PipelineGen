// Package app — minimal RenderingGen composition. This file deliberately
// owns only overlay rendering dependencies; it must not grow creator, DB,
// Qdrant, scheduler, or application-Drive dependencies.
package wiring

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	worker "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/worker"
	capoverlays "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	infraoverlays "github.com/Marcuss-ops/PipelineGen/internal/platform/overlays"
	"go.uber.org/zap"
)

// DefaultGPUGateSlots is the measured overlay GPU concurrency: three
// concurrent holders, i.e. the ceiling after retuning 2026-09-27.
//
// 3 is the new certified value together with the GPU peers:
//
//   - RenderingGen's worker `gpu_lanes: 3` (was 2) after queue-wait audit
//     showed 62% wall at width 4 behind 2 lanes; throttling overlay pool
//     4→2 + raising lanes 2→3 keeps pipelining while cutting queue wait;
//   - a Chronon video job is itself mutex-serialized by the daemon
//     execution-domain contract
//     (Chronon3d/apps/chronon3d_cli/daemon/daemon_render_concurrency.hpp),
//     so the gain is admission concurrency (less queue wait), not overlapping
//     Chronon execution. RTX A4000 VMA peak 451 MB @ 16 GB leaves headroom.
//
// Every process sharing the GPU must therefore be given the SAME
// RENDERINGGEN_GPU_SLOTS value (see overlays.GPUGate). The shipped
// systemd drop-in `scripts/systemd/pipelinegen.service.d/gpu-slots.conf`
// pins RENDERINGGEN_GPU_SLOTS=3 to keep the contract byte-identical.
const DefaultGPUGateSlots = infraoverlays.DefaultGPUGateSlots

// resolveGPUGateSlots parses RENDERINGGEN_GPU_SLOTS. `explicit` reports whether
// an operator actually configured a value, so the caller can distinguish "the
// default applied" from "the operator chose one slot"; an unparsable or
// out-of-range value falls back to the shared default and is reported as not explicit.
func resolveGPUGateSlots(raw string) (slots int, explicit bool) {
	return infraoverlays.ResolveGPUGateSlots(raw)
}

// gpuGateSlots reads the overlay GPU slot count from the environment.
func gpuGateSlots() (slots int, explicit bool) {
	return resolveGPUGateSlots(os.Getenv("RENDERINGGEN_GPU_SLOTS"))
}

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
	gate, lockPath, slots, err := newOverlayGPUGate()
	explicitSlots := strings.TrimSpace(os.Getenv("RENDERINGGEN_GPU_SLOTS")) != ""
	if err != nil {
		return nil, nil, err
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
	if err := reg.Register(capoverlays.JobTypePrepare, handlers.Prepare); err != nil {
		return nil, nil, err
	}
	// P3-5: il render locale si disattiva solo via gate esplicito dopo
	// verifica consumer (durable resolver). Default ancora abilitato.
	if LocalOverlayRenderEnabled() {
		if err := reg.Register(capoverlays.JobTypeRender, handlers.Render); err != nil {
			return nil, nil, err
		}
		reg.SetProducesArtifacts(capoverlays.JobTypeRender, true).Freeze()
	} else {
		log.Info("local overlay.render disabled: renders go through RenderingGen queue/worker only")
		reg.Freeze()
	}
	caps := appjobs.WorkerCapabilities{JobTypes: reg.JobTypes(), GPU: true, FFmpeg: true}
	cleanup := func() { _ = os.RemoveAll(workspace.Root) }
	return &RenderingRuntime{Registry: reg, Caps: caps, Workspace: workspace, Cache: cache, Log: log}, cleanup, nil
}
