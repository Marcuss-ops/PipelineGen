// Package voiceover — planner.go (PR-VOICEOVER-BOUNDED-EXECUTOR, Blocco 3, June 2026).
//
// Plan materialises the per-language Tasks for the bounded parallel
// executor. Per-language side-data (filename, voice override,
// voiceover ID) is computed up-front so the worker goroutine only
// does the per-language fan-out (TTS → post-process → upload →
// atomic swap + outbox) inside processOneTask.
//
// Plan output is immutable: the executor never mutates Tasks. The
// canonical inputs are the *GenerateVoiceoversCommand and the
// *ResolvedDestination (already resolved by Execute once per batch).
//
// textHash and requestID are batch-level constants: Plan computes them
// once and threads the same values into every Task so every row in
// the batch shares the same request_id / text_hash / language-key
// triplet.
package voiceover

import (
	"fmt"
)

// Plan materialises []Task for the per-item fan-out. Returns the
// tasks slice, the per-batch requestID, and an empty string for the
// legacy per-batch textHash slot (Step 5: textHash is now per-item,
// threaded into each Task.TextHash).
//
// Step 5 (P0.3 items-model recovery, June 2026): Plan iterates
// cmd.Items (not cmd.Languages — that field was removed from
// GenerateVoiceoversCommand). Each item carries its own
// text/language/voice/filename so per-task data is sourced from
// the item directly. textHash is computed per-item (mixed-text
// invariant: each row's text_hash column reflects THAT item's text,
// not the batch's text). requestID remains a batch-level constant
// (the same value threads into every child row so audit queries
// correlate the batch by request_id).
//
// Empty cmd.Items returns (nil, "", "") — the executor's Run
// short-circuits cleanly when len(tasks)==0 (no sem channel alloc).
//
// Plan is the canonical planning hook for Blocco 7 — adding a
// pre-compute step (e.g. semantic-tagger pre-warm, hash pre-compute)
// at planning time keeps the executor hot path tight.
func (u *GenerateVoiceoversUseCase) Plan(
	cmd *GenerateVoiceoversCommand,
	dest *ResolvedDestination,
) ([]Task, string, string) {
	if cmd == nil || len(cmd.Items) == 0 {
		return nil, "", ""
	}

	requestID := buildRequestID()

	folderID := ""
	if dest != nil {
		folderID = dest.FolderID
	}

	tasks := make([]Task, len(cmd.Items))
	for i, itemSpec := range cmd.Items {
		// Per-item textHash: each item's text is independent so the
		// hash reflects THIS item's text (Step 5 invariant — mixed
		// texts are first-class and each row's text_hash must match).
		//
		// PR-VO-TYPED-PRIMITIVES (July 2026): the canonical impl
		// lives in voiceover/texthash.go::ComputeTextHash. The
		// pre-refactor planner.go::truncationHash + jobs/fanout.go::
		// textHashSHA256 duplicates are collapsed to this single
		// source of truth (byte-equivalent with both — see the
		// audit-pin in the canonical impl's package doc).
		perItemTextHash := ComputeTextHash(itemSpec.Text)
		// E4: buildCommandFilenameForItem → canonical BuildVoiceoverFilename.
		// Inputs are pre-validated by itemSpec via the higher-layer
		// GenerateVoiceoversCommand.Validate gate.
		//
		// PR-VO-TYPED-PRIMITIVES (July 2026): perItemTextHash is the
		// typed TextHash envelope. FilenameSpec.TextHash + buildVoiceoverID
		// first param are raw string (explicit string() conversion at
		// the seam). PR-VO-TEXTHASH-64: the envelope now carries the
		// full 64-char SHA-256.
		filename, err := BuildVoiceoverFilename(FilenameSpec{
			Text:     itemSpec.Text,
			Language: itemSpec.Language,
			TextHash: string(perItemTextHash),
			Template: itemSpec.Filename,
		})
		if err != nil {
			panic(fmt.Sprintf("voiceover.BuildVoiceoverFilename (Plan): %v (item=%+v)", err, itemSpec))
		}
		id := buildVoiceoverID(string(perItemTextHash), itemSpec.Language, folderID)
		tasks[i] = Task{
			Index:         i,
			Language:      itemSpec.Language,
			VoiceOverride: itemSpec.Voice,
			Filename:      filename,
			ID:            id,
			RequestID:     requestID,
			TextHash:      perItemTextHash,
			Destination:   dest,
			Command:       cmd,
		}
	}
	return tasks, requestID, ""
}

// truncationHash — REMOVED in PR-VO-TYPED-PRIMITIVES (July 2026).
// The canonical impl now lives at voiceover/texthash.go::
// ComputeTextHash. The pre-refactor function was a duplicate of
// jobs/fanout.go::textHashSHA256 (byte-equivalent per the
// audit-pin) and the consolidation collapses both into the typed
// envelope + canonical impl. The audit-pin comment block above
// (Per-item textHash MUST stay in lock-step with …) is preserved
// on the call site in Plan() so future readers see the original
// invariant context.
