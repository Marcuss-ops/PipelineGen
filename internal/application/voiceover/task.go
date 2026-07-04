// Package voiceover — task.go (PR-VOICEOVER-BOUNDED-EXECUTOR, Blocco 3, June 2026).
//
// The Task struct is the immutable unit of work for the bounded parallel
// executor (executor.go). Tasks are materialised once by planner.go (Plan)
// and consumed by executor.go (Run); per-language side-data (filename,
// voice override, voiceover ID) is computed up-front so the worker
// goroutine only does the per-language fan-out (TTS → post-process →
// upload → atomic swap + outbox).
//
// TaskFn is the per-task worker signature: implementations MUST NOT
// panic on their own — the executor's Run wraps the call in a panic
// recover that surfaces the panic as a StatusFailed result. The closure
// wiring in NewGenerateVoiceoversUseCase binds TaskFn to
// processOneTask(ctx, t) at construction time so no per-call wiring
// overhead exists inside the executor.
package voiceover

import "context"

// Task is the immutable per-language unit of work materialised by
// planner.go and consumed by executor.go. All fields are computed
// once at planning time; Run() does NOT mutate Tasks.
type Task struct {
	// Index is the position in the input cmd.Languages slice — used
	// by the executor to write results[t.Index] for language-locked
	// output ordering (irrespective of completion order).
	Index int

	// Language is the BCP-47 code to synthesise. Typed (Language)
	// per PR-VO-TYPED-PRIMITIVES — JSON wire + DB column are
	// byte-equivalent with the pre-refactor string field.
	Language Language

	// VoiceOverride is the per-language voice (cmd.VoiceOverrides[Language]).
	// Empty means "use TTSProvider's default voice".
	VoiceOverride string

	// Filename is the sanitized per-language filename (with the
	// canonical {slug}_{lang}.mp3 token grammar applied).
	Filename string

	// ID is the canonical voiceover row identifier
	// (buildVoiceoverID(textHash, language, folderID)).
	ID string

	// RequestID is the per-batch identifier so every row in the same
	// batch shares the same request_id column value.
	RequestID string

	// TextHash is the per-batch content hash (ComputeTextHash of
	// itemSpec.Text). All rows in this batch share the same hash.
	// Typed (TextHash) per PR-VO-TYPED-PRIMITIVES.
	TextHash TextHash

	// Destination is the resolved destination shared across all
	// languages in this batch (FolderID + FolderPath + StyleGroup).
	Destination *ResolvedDestination

	// Command is the canonical Command that produced this Task. The
	// worker reads Text / RemoveSilence / Metadata / Strategy from
	// here. Holding it as a pointer keeps the Plan output immutable
	// and avoids re-reading the same fields from the orchestrator.
	Command *GenerateVoiceoversCommand
}

// TaskResult is the per-task outcome — alias for VoiceoverItemResult
// so the executor returns the canonical shape without a second type.
type TaskResult = VoiceoverItemResult

// TaskFn is the per-task worker signature. Implementations MUST NOT
// panic on their own; the executor's Run provides panic isolation.
// Returning StatusFailed in the TaskResult with a populated Error field
// is the canonical "soft failure" path.
type TaskFn func(ctx context.Context, task Task) TaskResult

// ProgressFunc is the optional per-result progress callback. nil-safe;
// the executor skips the call when nil. Called once per task AFTER
// the worker returns (NOT after Start). The ctx passed in is the
// same ctx the worker observed — callers wiring JobTools.Progress
// can call it and trust the ctx is the post-task state.
type ProgressFunc func(ctx context.Context, res TaskResult)
