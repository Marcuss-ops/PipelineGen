// Package adapters — voiceover_scene_fanout.go (PR 9 follow-up, June 2026).
//
// Canonical per-scene voiceover fanout for the voiceover postprocessor
// and the async job worker path. Two consumers drive the same fanout:
//
//   - internal/application/scripts/adapters/processor_voiceover.go
//     (PostProcess path; ProcessorPolicy = BestEffort — failures
//     collect as warnings, not errors).
//   - internal/application/scripts/jobs/job_helpers.go
//     (async job worker path; the same fanout feeds the canonical
//     outbox event payload shape).
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

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// VoiceoverSceneInput is the canonical per-scene fanout input.
// SceneIndex is the canonical stable scene index from
// engineResult.Output.SpecScene.Scenes (PR 9 contract). Filename is
// pre-sanitised via voiceover.SanitizeBasename to reject path
// separators and unsafe characters. Destination is the optional typed
// routing request (FolderID / Group / SubfolderName + StyleGroup); nil
// means "no destination override" (caller-supplied or config-level
// fallback).
type VoiceoverSceneInput struct {
	SceneIndex  int
	Text        string
	Filename    string
	Destination *voiceover.DestinationRequest
}

// SceneOutcome is the canonical per-scene fanout return shape. Status
// carries the canonical compiled string ("completed" or "failed"; the
// processor's ProcessorBestEffort policy is the source of truth for
// this string). On success Link + LocalPath carry the production
// concrete values from the typed *voiceover.VoiceoverResult (post-PR 7
// M2 typed-return refactor — no interface{} / no type assertions).
type SceneOutcome struct {
	SceneIndex int
	Status     string
	Link       string
	LocalPath  string
	// Error carries the error message when Status == "failed"; empty otherwise.
	Error string
}

// RunVoiceoverSceneFanout fans out a slice of VoiceoverSceneInput to
// the canonical VoiceoverService port with bounded concurrency,
// returning one *SceneOutcome per input (in the SAME slice order).
//
// Per-scene failures do NOT abort the batch — canonical
// ProcessorBestEffort semantics: each failure surfaces as a
// {Status: "failed", Error: err.Error()} outcome so the processor's
// warning-collector (and the job worker's audit log) sees the full
// picture rather than silently dropping siblings.
//
// Concurrency is clamped to >= 1 so an invalid caller arg (0 or
// negative) doesn't crash ParallelMap's goroutine pool.
func RunVoiceoverSceneFanout(ctx context.Context, gen VoiceoverService, language string, items []VoiceoverSceneInput, concurrency int) []*SceneOutcome {
	if concurrency < 1 {
		concurrency = 1
	}
	return concurrent.ParallelMap(items, concurrency, func(idx int, item VoiceoverSceneInput) *SceneOutcome {
		out := &SceneOutcome{SceneIndex: item.SceneIndex}
		// Per-item panic-recover: a misbehaving fake (e.g. nil-typed
		// service, malformed Destination) surfaces as a failed outcome
		// rather than crashing ParallelMap's goroutine pool.
		defer func() {
			if r := recover(); r != nil {
				out.Status = "failed"
				out.Error = "voiceover fanout panic"
			}
		}()

		// Both call paths return the typed *voiceover.VoiceoverResult
		// (post-Step 7 M2 typed-return refactor, June 2026) — no
		// type assertion, no extractVoiceoverPaths helper.
		var result *voiceover.VoiceoverResult
		var err error
		if item.Destination != nil {
			result, err = gen.GenerateWithDestination(ctx, item.Text, language, item.Filename, item.Destination)
		} else {
			result, err = gen.Generate(ctx, item.Text, language, item.Filename)
		}
		if err != nil {
			out.Status = "failed"
			out.Error = err.Error()
			return out
		}
		out.Status = "completed"
		if result != nil {
			out.Link = result.DriveLink
			out.LocalPath = result.Path
		}
		return out
	})
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
