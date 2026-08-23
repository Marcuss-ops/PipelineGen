// Package voiceover defines the canonical domain types for
// voiceover generation.
//
// Fase 4 Spina Dorsale (July 2026): three territories separated —
//
//  1. VoiceoverSynthesis    — TTS only (no database/sql, no drive, no qdrant)
//  2. VoiceoverPublication  — Drive upload via delivery.Publisher
//  3. VoiceoverFinalization  — SQLite persistence + lifecycle projection + outbox
package voiceover

import "time"

// ────────────────────────────────────────────────────────────────────
// VoiceoverSynthesisResult — TTS-only output (Fase 4 Spina Dorsale)
// ────────────────────────────────────────────────────────────────────
//
// VoiceoverSynthesisResult captures everything the TTS synthesizer
// produces. It carries ZERO Drive fields, ZERO lifecycle fields —
// only the local audio payload, its identity, and basic acoustic
// metadata.
//
// Territory: VoiceoverSynthesis (TTS only).
//   - NO database/sql import
//   - NO drive import
//   - NO qdrant import
//
// Callers obtain this from TTSProvider.Synthesize (application layer).
type VoiceoverSynthesisResult struct {
	// Text is the input text that was converted (echoed for traceability).
	Text string

	// Locale is the BCP-47 language tag used (normalized).
	Locale string

	// Voice is the resolved TTS voice name actually used.
	Voice string

	// Filename is the computed filename.
	Filename string

	// LocalPath is the absolute path to the generated audio file on disk.
	LocalPath string

	// CleanedPath is the post-AudioPostProcessor silence-removed path.
	// Empty when no audio cleanup was performed.
	CleanedPath string

	// LegacyFileMD5 is the SHA-256 hash of the generated audio file.
	LegacyFileMD5 string

	// FileSize is the size of the generated audio file in bytes.
	FileSize int64

	// Duration is the audio duration in seconds, if measured.
	Duration float64
}

// ────────────────────────────────────────────────────────────────────
// Result — composite canonical output (Fase 4 Spina Dorsale)
// ────────────────────────────────────────────────────────────────────
//
// Result is the canonical, fully-typed output of a voiceover generation.
// Every call site receives this struct — no any, no map[string]any.
//
// Fields map 1:1 to the lifecycle steps of the use case:
//
//	TTS → file write → hash → (optional) Drive upload → lifecycle finalize
//
// Fase 4 (July 2026): embeds VoiceoverSynthesisResult as a promoted
// sub-field. Callers can access synthesis fields directly (result.LocalPath,
// result.Voice, etc.) OR via the named field (result.Synthesis.LocalPath).
type Result struct {
	// ── Identity ──

	// OK is true when the generation completed without error.
	OK bool

	// ID is the deterministic command ID (matches GenerateVoiceoverCommand.ID()).
	ID string

	// ── Synthesis territory (embedded; fields promoted to Result) ──

	VoiceoverSynthesisResult

	// ── Publication territory (Drive fields) ──

	// DriveLink is the Google Drive web view link.
	DriveLink string

	// DriveFileID is the Google Drive file ID.
	DriveFileID string

	// DownloadLink is the direct download link from Drive.
	DownloadLink string

	// UploadedToDrive is true when the file was successfully uploaded.
	UploadedToDrive bool

	// ── Finalization territory (Lifecycle) ──

	// Status is the canonical lifecycle state (e.g. "generated", "uploaded").
	Status string

	// CreatedAt is the time the voiceover record was created.
	CreatedAt time.Time

	// ── Cross-cutting ──

	// Warnings carries non-fatal observations (e.g. "hash computation skipped",
	// "drive upload timed out but local file is fine"). Never nil.
	Warnings []string
}

// AddWarning appends a non-fatal warning. Safe to call on a nil receiver
// (no-op) so callers can chain without nil checks.
func (r *Result) AddWarning(msg string) {
	if r == nil {
		return
	}
	r.Warnings = append(r.Warnings, msg)
}

// VoiceoverResult is the canonical typed alias of Result, introduced to fix
// the undefined: domain.VoiceoverResult build break at services.go:97 per
// architecture/current.yaml#id-26 follow_up_tickets.PR-VOICEOVER-STREAM-SUPERSESSION-2026-06-28.
// Wave 21 PR-G.2 BACKFILL (deadline 2026-07-10) is the planned window for
// the deep typed-port push; this alias is a tactical unblock while PR-G.2 ships.
type VoiceoverResult = Result
