// Package adapters — voiceover_scene_fanout.go (P0-#3 final closure, July 2026).
//
// Canonical per-scene voiceover fanout for the voiceover postprocessor
// and the async job worker path. Two consumers drive the same fanout:
//
//   - internal/capabilities/scripts/adapters/processor_voiceover.go
//     (PostProcess path; ProcessorPolicy = BestEffort — failures
//     collect as warnings, not errors).
//   - internal/capabilities/scripts/jobs/job_helpers.go
//     (async job worker path; the same fanout feeds the canonical
//     outbox event payload shape).
//
// Port cutover (P0-#3 final closure, July 2026): the legacy local
// `VoiceoverService` interface (Generate + GenerateWithDestination,
// positional signature) is RETIRED. The fanout now delegates to the
// canonical `voiceover.VoiceoverItemExecutor` port — the SAME per-item
// pipeline the voiceover.generate_item child job and the
// promoVoiceoverAdapter already route through (P0-#3 commits f2779494b
// + 6e2634d82). The "Generate" vs "GenerateWithDestination" branch
// (the per-item Destination-nil dispatch) is COLLAPSED: the use case's
// `ResolveDestinationWithFallback` correctly handles `cmd.Destination
// == nil` via the canonical DefaultFolderResolver fallback, so the
// caller doesn't need a port-level branch. Result: one method, one
// type system surface, no per-caller dispatch logic.
//
// Why pkg/concurrent.ParallelMap (over .Map or .WithContext + SafeGo):
//
//   - .Map aborts the context on the first error, short-circuiting
//     siblings. Voiceover is best-effort; we need to collect ALL outcomes,
//     not abort on the first failure.
//   - .WithContext + SafeGo iterates manually and adds boilerplate for
//     per-item error capture.
//   - ParallelMap preserves SLICE ORDER (idx -> outcome mapping) which
//     matches the canonical engineResult.Output.SpecScene.Scenes order
//     (PR 9 contract). The processor relies on slice order to attach
//     outcomes back to scenes.
//
// The internal panic-recover wraps the per-item fn so a single bad item
// (e.g. malformed Destination, nil-typed service from a misconfigured
// fake) can't crash the ParallelMap goroutine pool — it surfaces as
// a failed SceneOutcome like any normal error.
package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// VoiceoverSceneInput is the canonical per-scene fanout input.
// SceneIndex is the canonical stable scene index from
// engineResult.Output.SpecScene.Scenes (PR 9 contract). Filename is
// pre-sanitised via voiceover.SanitizeBasename to reject path
// separators and unsafe characters. Destination is the optional typed
// routing request (FolderID / Group / SubfolderName + StyleGroup); nil
// means "no destination override" — the per-item use case's
// `ResolveDestinationWithFallback` (canonical PR 6 P0.2, June 2026)
// short-circuits to "missing_folder_id" so the use case still
// fail-closes (no /tmp fallback, no silent write to the default root
// when no destination is wired at composition time).
type VoiceoverSceneInput struct {
	SceneIndex  int
	Text        string
	Voice       string
	Filename    string
	Destination *voiceover.DestinationRequest

	// Moments are the optional LLM-produced annotation queries (kind +
	// value) to anchor onto the canonical word timing. The model provides
	// only text; timestamps are derived deterministically via PhraseLocator.
	Moments []audio.MomentQuery

	// Timing is the canonical voiceover timing policy for this scene.
	// nil means the canonical defaults apply (best_effort / word / [json]);
	// required makes the per-item pipeline fail closed on missing/invalid
	// timing or silence-trim without an edit map.
	Timing *audio.TimingRequest
}

// SceneOutcome is the canonical per-scene fanout return shape. Status
// carries the canonical compiled string ("completed" or "failed"; the
// processor's ProcessorBestEffort policy is the source of truth for
// this string). On success Link + LocalPath carry the production
// concrete values from the typed *voiceover.VoiceoverItemResult
// (post-P0-#3 cutover; same fields as the legacy *VoiceoverResult
// after the per-item fanout migration).
type SceneOutcome struct {
	SceneIndex int
	Status     string
	Link       string
	LocalPath  string
	DurationMs int64
	// Error carries the error message when Status == "failed"; empty otherwise.
	Error string

	// Timing carries the per-item timing bundle references when the
	// pipeline produced one (nil when timing is disabled). Forwarded so
	// the scene binding can expose timing links per language.
	Timing *voiceover.VoiceoverTimingResult
}

// RunVoiceoverSceneFanout fans out a slice of VoiceoverSceneInput to
// the canonical voiceover.VoiceoverItemExecutor port with bounded
// concurrency, returning one *SceneOutcome per input (in the SAME
// slice order).
//
// P0-#3 cutover (July 2026): the executor is the canonical narrow
// port (AGENTS.md Pattern 0 — port abstraction layer, June 2026). The
// concrete production implementation is
// *voiceover.ProcessVoiceoverItemUseCase (constructed once at
// composition time in `build_bundles_voiceover.go`). Test doubles
// inject stubs that record invocations + per-call results.
//
// Per-scene failures do NOT abort the batch — canonical
// ProcessorBestEffort semantics: each failure surfaces as a
// {Status: "failed", Error: err.Error()} outcome so the processor's
// warning-collector (and the job worker's audit log) sees the full
// picture rather than silently dropping siblings.
//
// Concurrency is clamped to >= 1 so an invalid caller arg (0 or
// negative) doesn't crash ParallelMap's goroutine pool.
//
// RequestID strategy: pre-computed ONCE per batch from
// `ctx.Value("script_job_id")` → `corid.FromContext(ctx)` → fallback
// to a synthetic `scene-fanout-<timestamp>` ID. The same value is
// threaded into every per-item GenerateVoiceoverItemCommand as both
// RequestID and ParentJobID (mirrors the promo path's
// `req.RequestID == req.ParentJobID` convention — see
// `voiceover/promo_test.go::TestPromoVoiceoverAdapter_P0_3_Contract`).
// The per-item use case treats ParentJobID as the parent job
// identifier for aggregator correlation, so the synchronous
// scene_fanout uses the same value to satisfy that invariant. The
// per-item textHash (computed via `voiceover.ComputeTextHash`) varies
// per scene, so the (ParentJobID, Language, TextHash) tuple remains
// unique across siblings even though ParentJobID is shared — no DB
// collision risk.
func RunVoiceoverSceneFanout(ctx context.Context, executor voiceover.VoiceoverItemExecutor, language string, items []VoiceoverSceneInput, concurrency int) []*SceneOutcome {
	if concurrency < 1 {
		concurrency = 1
	}
	requestID := resolveSceneFanoutRequestID(ctx)

	return concurrent.ParallelMap(items, concurrency, func(idx int, item VoiceoverSceneInput) *SceneOutcome {
		out := &SceneOutcome{SceneIndex: item.SceneIndex}
		// Per-item panic-recover: a misbehaving fake (e.g. nil-typed
		// executor, malformed Destination) surfaces as a failed outcome
		// rather than crashing ParallelMap's goroutine pool.
		defer func() {
			if r := recover(); r != nil {
				out.Status = "failed"
				out.Error = "voiceover fanout panic"
			}
		}()

		// Per-item text fingerprint: 16-hex-char SHA-256 prefix per
		// voiceover/texthash.go::ComputeTextHash. Pre-computed here so
		// the per-item use case's finalizer writes the same value into
		// the voiceovers.text_hash column — matches the canonical
		// shape from the voiceover.generate_item child job path.
		textHash := voiceover.ComputeTextHash(item.Text)

		// Project is read verbatim from the canonical destination routing
		// context. It never invents a "scene" fallback: an empty project
		// fails closed at the publisher (ErrVoiceoverPublishProjectRequired).
		project := ""
		if item.Destination != nil {
			project = item.Destination.Project
		}

		// Build the canonical per-item command. The SAME shape the
		// voiceover.generate_item job handler and the
		// promoVoiceoverAdapter build (per voiceover/command.go).
		itemCmd := &voiceover.GenerateVoiceoverItemCommand{
			ParentJobID: requestID, // same as RequestID — sync path, no dispatcher
			RequestID:   requestID,
			Text:        item.Text,
			Language:    voiceover.Language(language),
			Voice:       item.Voice,
			Filename:    item.Filename,
			TextHash:    textHash,
			Destination: item.Destination, // nil-safe at the use case boundary
			Project:     project,
			Strategy:    "replace", // canonical default (matches pre-P0-#3 Service.GenerateWithDestination default)
			// Every generated scene goes through the canonical post-TTS
			// cleanup; the media executor removes silence runs longer than
			// 800 ms before the scene duration is published.
			RemoveSilence: true,
			Moments:       item.Moments,
			Timing:        item.Timing,
		}

		// Execute the per-item pipeline (TTS → publish → finalize).
		// Real failures surface as typed Go errors (no Result{OK:false}
		// masking — the per-item use case is the canonical
		// fail-closed-by-error surface, same contract as
		// voiceover/promo.go's promoVoiceoverAdapter).
		result, err := executor.Execute(ctx, itemCmd)
		if err != nil {
			out.Status = "failed"
			out.Error = err.Error()
			return out
		}

		// Per-item use case returns (result, nil) on success. The
		// canonical partial-failure shape — Status: StatusFailed with
		// Error populated — is the FAILURE PATH and surfaces as the
		// `err != nil` branch above. Defense-in-depth: if a future
		// refactor relaxes that contract and returns (partialResult,
		// nil) with result.Status == StatusFailed, surface the inline
		// error string here so the canonical SceneOutcome.Error
		// channel still carries the partial-failure signal to the
		// processor's warning collector.
		if result != nil && result.Status == voiceover.StatusFailed {
			out.Status = "failed"
			if result.Error != "" {
				out.Error = result.Error
			} else {
				out.Error = "voiceover item returned StatusFailed with empty error"
			}
			return out
		}

		out.Status = "completed"
		if result != nil {
			out.Link = result.DriveLink
			out.LocalPath = result.LocalPath
			out.DurationMs = result.DurationMs
			out.Timing = result.Timing
		}
		return out
	})
}

// resolveSceneFanoutRequestID derives a stable per-batch RequestID
// from the request context. The synchronous scene_fanout has no
// dispatcher to generate a canonical correlation ID, so we fall
// through three sources in priority order:
//
//  1. `ctx.Value("script_job_id")` — set by the postprocessor
//     processor_voiceover.go (which forwards corid.FromContext
//     when the value is absent) and by the script job worker.
//  2. `corid.FromContext(ctx)` — the canonical request correlation
//     ID middleware (pkg/corid).
//  3. `scene-fanout-<unix-nano>` — synthetic fallback. NEVER call
//     voiceover.buildRequestID here: that helper generates a
//     different ID on every call, which would disconnect the
//     per-scene children from each other in the voiceovers table
//     (the audit P0.1 pattern: API request_id (A) → fanout
//     generates B → children lose correlation). The synthetic
//     fallback uses time.Now().UnixNano() so the value is
//     deterministic for a given run, distinct across runs, and
//     traceable in logs (it carries the run start time).
func resolveSceneFanoutRequestID(ctx context.Context) string {
	if val, ok := ctx.Value("script_job_id").(string); ok && val != "" {
		return val
	}
	if cid := corid.FromContext(ctx); cid != "" {
		return cid
	}
	return fmt.Sprintf("scene-fanout-%d", time.Now().UnixNano())
}

// CountCompletedSceneOutcomes returns the count of outcomes whose
// Status is NOT "failed" (canonical inverse of failure count). The
// canonical policy contract is "any non-failed status is a successful
// outcome" — matches the processor's "Status == 'failed'" warning filter.
func CountCompletedSceneOutcomes(outcomes []*SceneOutcome) int {
	var count int
	for _, out := range outcomes {
		if out.Status != "failed" {
			count++
		}
	}
	return count
}
