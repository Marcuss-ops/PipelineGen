package voiceover

import "time"

// Result is the canonical, fully-typed output of a voiceover generation.
// Every call site receives this struct — no interface{}, no map[string]any.
//
// Fields map 1:1 to the lifecycle steps of the use case:
//
//	TTS → file write → hash → (optional) Drive upload → lifecycle finalize
type Result struct {
	// OK is true when the generation completed without error.
	OK bool

	// ID is the deterministic command ID (matches GenerateVoiceoverCommand.ID()).
	ID string

	// Locale is the BCP-47 language tag used (normalized).
	Locale string

	// Voice is the resolved TTS voice name actually used.
	Voice string

	// Text is the input text that was converted (echoed for traceability).
	Text string

	// Filename is the computed filename (matches GenerateVoiceoverCommand.Filename()).
	Filename string

	// LocalPath is the absolute path to the generated audio file on disk.
	LocalPath string

	// FileHash is the SHA-256 hash of the generated audio file.
	FileHash string

	// FileSize is the size of the generated audio file in bytes.
	FileSize int64

	// Duration is the audio duration in seconds, if measured.
	Duration float64

	// ── Drive fields (populated only when Destination.FolderID is non-empty) ──

	// DriveLink is the Google Drive web view link.
	DriveLink string

	// DriveFileID is the Google Drive file ID.
	DriveFileID string

	// DownloadLink is the direct download link from Drive.
	DownloadLink string

	// UploadedToDrive is true when the file was successfully uploaded.
	UploadedToDrive bool

	// ── Lifecycle ──

	// Status is the canonical lifecycle state (e.g. "generated", "uploaded").
	Status string

	// CreatedAt is the time the voiceover record was created.
	CreatedAt time.Time

	// ── Warnings ──

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
