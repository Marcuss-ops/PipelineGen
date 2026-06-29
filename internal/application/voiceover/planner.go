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
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
)

// Plan materialises []Task for the per-language fan-out. Returns the
// tasks slice, the per-batch requestID, and the per-batch textHash.
//
// Empty cmd.Languages returns (nil, "", "") — the executor's Run
// short-circuits cleanly when len(tasks)==0 (no sem channel alloc).
//
// Plan is the canonical planning hook for Blocco 7 — adding a
// pre-compute step (e.g. semantic-tagger pre-warm, hash pre-compute)
// at planning time keeps the executor hot path tight.
func (u *GenerateVoiceoversUseCase) Plan(
	cmd *GenerateVoiceoversCommand,
	dest *ResolvedDestination,
) ([]Task, string, string) {
	if cmd == nil || len(cmd.Languages) == 0 {
		return nil, "", ""
	}

	requestID := buildRequestID()
	textHash := hashutil.SHA256String(cmd.Text)

	folderID := ""
	if dest != nil {
		folderID = dest.FolderID
	}

	tasks := make([]Task, len(cmd.Languages))
	for i, lang := range cmd.Languages {
		voice := ""
		if cmd.VoiceOverrides != nil {
			voice = cmd.VoiceOverrides[lang]
		}
		filename := u.buildCommandFilename(cmd, lang, textHash)
		id := buildVoiceoverID(textHash, lang, folderID)
		tasks[i] = Task{
			Index:         i,
			Language:      lang,
			VoiceOverride: voice,
			Filename:      filename,
			ID:            id,
			RequestID:     requestID,
			TextHash:      textHash,
			Destination:   dest,
			Command:       cmd,
		}
	}
	return tasks, requestID, textHash
}
