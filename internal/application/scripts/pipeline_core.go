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


