// Package scripts — pipeline_usecase is the orchestrator for the
// unified clip-source script-generation job (the heavy
// HandleClipScriptGenerateJob entry point).
//
// Wave 14 problem #4 (June 2026): previously ~280 LOC lived inline
// in api/script/handler_jobs.go::ScriptFlowHandler.HandleClipScriptGenerateJob:
//   - semaphore acquire/release
//   - payload decode
//   - scenes/doc services construction (two inline NewXxxService
//     calls)
//   - 3-way switch (clip-explicit / auto-search / text-only)
//   - prewarm goroutine launch
//   - pipeline.Run invocation
//   - buildFinalResult shaping
//   - return contract on the happy path
//
// Each of these belonged to the application layer:
//   - transport (handler) only reads the job payload, signals
//     sem/prewarm lifecycle, and calls the use case;
//   - orchestration (PipelineUseCase) decodes the payload, dispatches
//     to one of the three paths, calls the existing Pipeline.Run, and
//     shapes the response map.
//
// The use case owns:
//   - payload decoding (scriptpkg.DecodeGeneratePayload)
//   - 3-way switch with explicit failure messages per branch
//   - phase 2-4 invocation via the existing *Pipeline (pre-built
//     by composition with scenes + docs + post-gen callback wired)
//   - final-result-map shaping (buildFinalResult)
//   - HandleJob wrapper that acquires sem + kicks prewarm
//   - RegisterJobs hook so the handler no longer owns registration
//
// The use case does NOT own:
//   - the semaphore acquire/release (delegates to SemaphoreUseCase via
//     HandleJob)
//   - the prewarm goroutine (delegates to PrewarmUseCase via
//     HandleJob)
//   - HTTP transport shape, status codes, gin Error/OK helpers
//     (handler responsibility)
//   - the script-engine choice of model / language / tone (engine's
//     WriteScript's responsibility)
package scripts

import (
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// ErrInvalidPayload is the sentinel for "the JSON payload the worker
// forwarded to us could not be decoded". Maps to a job-system
// permanent failure (no retry).
var ErrInvalidPayload = errors.New("pipeline: invalid job payload")

// ErrBrokerNotSatisfied is the sentinel for "the caller supplied a
// jobsSvc value that does not implement the canonical Broker port".
// Returned from RegisterJobs when the type-assertion to Broker fails
// on a non-nil input. Maps to a job-system permanent failure (no retry):
// the composition root wired the wrong shape, retrying the same input
// will not recover. Replaces the silent-skip behavior of the prior
// structural-interface widening, giving first-integration-test
// feedback instead of a missing-handler surprise at first job
// dispatch.
var ErrBrokerNotSatisfied = errors.New("pipeline: jobsSvc does not satisfy Broker port")

// ErrClipPipelineUnavailable is the sentinel for "the request gave
// explicit ClipIDs but no ClipSourceBuilder is wired". Maps to a
// typed 503-class error in the handler.
var ErrClipPipelineUnavailable = errors.New("pipeline: clip pipeline unavailable")

// ErrAutoSearchUnavailable is the sentinel for "the request asked
// for num_clips but no MediaCurator is wired".
var ErrAutoSearchUnavailable = errors.New("pipeline: auto-search pipeline unavailable")

// ErrPipelineGenerationFailed is the generic wrapper for any failure
// inside Run. The inner error is accessible via errors.Unwrap.
var ErrPipelineGenerationFailed = errors.New("pipeline: generation failed")

// Phase 2 activation (June 2026) — ErrSceneImagesUnavailable is the
// sentinel for "the request asked for scene images (spec.GenerateSceneImages=true)
// but imageService was not wired at composition time, so
// PipelineUseCase was constructed with scenesReady=false". Maps to a
// 503-class error in the handler. Surfaces as a typed job failure
// (no retry — the wiring is missing) rather than silently producing
// empty scenes.
var ErrSceneImagesUnavailable = errors.New("pipeline: scene image generation unavailable (image service not wired)")

// PipelineUseCase orchestrates the unified clip-source job. The
// *Pipeline it holds is pre-built by composition (it already carries
// scenes-svc, docs-svc, postGen callback, and resolve-folder — so
// Reuse the existing application-layer infrastructure unchanged).
//
// Phase 2 activation (June 2026): added `scenesReady bool` flag so
// the use case can reject jobs asking for scene image generation
// when ImageService was not wired at composition time. Composition
// passes scenesReady = (scenesSvc != nil) which is true iff both
// scenesUC.Build() succeeded and docsSvc was non-nil (the existing
// outer gate). When false, Run returns ErrSceneImagesUnavailable
// before doing any work.
type PipelineUseCase struct {
	log          *zap.Logger
	engine       *Engine
	cfg          *configShim
	clipBuilder  *ClipSourceBuilder
	mediaCurator *MediaCurator
	semUC        *SemaphoreUseCase
	prewarmUC    *PrewarmUseCase
	pipeline     *Pipeline
	scenesReady  bool
}

// configShim wraps *config.Config so a nil cfg doesn't break the
// text-only path's defaults (previous handler's `if h.cfg != nil`
// guard). Avoids an `internal/platform/config` import in the
// use-case struct field while still letting the ctor receive a cfg.
type configShim struct {
	minWordFloor int
	ollamaModel  string
}

func newConfigShim(minWordFloor int, ollamaModel string) *configShim {
	return &configShim{minWordFloor: minWordFloor, ollamaModel: ollamaModel}
}

// NewPipelineUseCase wires the orchestrator. The constructor refuses
// to build if engine or pipeline is nil — those are the canonical
// components for the happy path. Other args (semUC, prewarmUC,
// clipBuilder, mediaCurator) may be nil; their absence is surfaced
// as a typed error at the dispatch step or as a no-op for prewarm.
//
// Composition root builds:
//
//	the *Pipeline via NewPipeline(... scenesUC.Build(...) ...
//	   documentsUC.DocumentsService() ... postGenClosure ...).
//
// This use-case receives that pre-built pointer.
//
// Phase 2 activation (June 2026): added scenesReady bool param. When
// false, the use case rejects any job that sets spec.GenerateSceneImages=true
// with typed ErrSceneImagesUnavailable — surfaces missing image wiring
// at first integration rather than silently producing empty scene
// arrays in the final result. Composition passes scenesReady = true
// iff scenesSvc was successfully built at composition time (i.e.
// ImageService was wired AND scenesUC.Build() succeeded).
func NewPipelineUseCase(
	log *zap.Logger,
	engine *Engine,
	minWordFloor int,
	ollamaModel string,
	clipBuilder *ClipSourceBuilder,
	mediaCurator *MediaCurator,
	semUC *SemaphoreUseCase,
	prewarmUC *PrewarmUseCase,
	pipeline *Pipeline,
	scenesReady bool,
) (*PipelineUseCase, error) {
	if engine == nil {
		return nil, fmt.Errorf("%w: engine is required", ErrPipelineGenerationFailed)
	}
	if pipeline == nil {
		return nil, fmt.Errorf("%w: pipeline is required", ErrPipelineGenerationFailed)
	}
	return &PipelineUseCase{
		log:          log,
		engine:       engine,
		cfg:          newConfigShim(minWordFloor, ollamaModel),
		clipBuilder:  clipBuilder,
		mediaCurator: mediaCurator,
		semUC:        semUC,
		prewarmUC:    prewarmUC,
		pipeline:     pipeline,
		scenesReady:  scenesReady,
	}, nil
}

// defaultsString is a tiny inline default-coalesce that mirrors
// pkg/defaults.String without taking the import in this file's path.
func defaultsString(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}

// Run executes the full clip-source job. The caller (HandleJob or a
// test) has already acquired a semaphore slot via SemaphoreUseCase
// and started prewarm via PrewarmUseCase; this method owns phase 1
// (path dispatch) + phase 2-4 (pipelines) + result shaping.
//
// Returns the result map (same shape as the old buildFinalResult
// output) on success, or a typed error otherwise.
