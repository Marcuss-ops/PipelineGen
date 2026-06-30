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

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
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
		// IMPORTANT: this MUST stay in lock-step with
		// internal/application/voiceover/jobs/fanout.go::textHashSHA256
		// (16 hex chars / 64-bit SHA-256 prefix). Two paths produce
		// text_hash column values for the same item — fanout.go writes
		// the child's JSON TextHash field, this Planner path writes the
		// Task.TextHash that processOneLanguage persists into the row.
		// A length drift here makes the voiceovers.text_hash column
		// inconsistent (16 chars vs 64 chars) and silently breaks the
		// Step 4 aggregator's dedupe. Step 5 audit-pin.
		perItemTextHash := truncationHash(itemSpec.Text)
		// E4: buildCommandFilenameForItem → canonical BuildVoiceoverFilename.
		// Inputs are pre-validated by itemSpec via the higher-layer
		// GenerateVoiceoversCommand.Validate gate.
		filename, err := BuildVoiceoverFilename(FilenameSpec{
			Text:     itemSpec.Text,
			Language: itemSpec.Language,
			TextHash: perItemTextHash,
			Template: itemSpec.Filename,
		})
		if err != nil {
			panic(fmt.Sprintf("voiceover.BuildVoiceoverFilename (Plan): %v (item=%+v)", err, itemSpec))
		}
		id := buildVoiceoverID(perItemTextHash, itemSpec.Language, folderID)
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

// truncationHash returns the first 16 hex chars of SHA-256(text).
// MUST stay byte-identical with
// internal/application/voiceover/jobs/fanout.go::textHashSHA256 so
// the per-child text_hash column is consistent across paths.
//
// INTERNAL CONTRACT: hashutil.SHA256String returns lowercase hex
// (Go stdlib encoding/hex.EncodeToString default). If that ever
// changes, this helper MUST produce uppercase OR the cross-package
// equality breaks silently. The reviewer-flagged duplication is
// closed by Step 12 cleanup (extract to voiceover/texthash.go) —
// until then this comment is the audit-pin.
//
// 16 hex chars = 64 bits of entropy, sufficient for collision
// resistance at expected row counts (~10^5 distinct texts).
func truncationHash(text string) string {
	return hashutil.SHA256String(text)[:16]
}
