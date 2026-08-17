// Package voiceover — result.go (PR-VOICEOVER-COMMAND-EXTRACT, June 2026).
//
// GenerateVoiceoversResult is the canonical singular Result the use case
// returns. The shape is aggregate-friendly: one summary at the top,
// per-item detail in PerLanguage[]. The partial-failure semantic is
// carried via the overall OK + per-item Status fields:
//
//   - resp.OK == true  iff SuccessCount == TotalOutputs.
//   - Per-item Status  is StatusCompleted | StatusFailed.
//   - Per-item Error   is non-empty ONLY when Status == StatusFailed.
//
// Execute returns (*Result, nil) on partial failure so the caller can
// decide whether to abort or accept the partial output (transport
// layer surfaces a 200 with `ok:false` body, NOT a 500 — the
// per-item failures are surfaced in the body so a client can
// retry just the failed languages).
package voiceover

import (
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// VoiceoverDestinationUnavailableCode is the stable machine-readable code
// emitted when the requested voiceover destination cannot be resolved.
// This is a hard failure: configured defaults and historical roots must
// never replace an explicit or otherwise unavailable destination.
const VoiceoverDestinationUnavailableCode = "VOICEOVER_DESTINATION_UNAVAILABLE"

// ErrVoiceoverDestinationUnavailable is the canonical destination
// availability sentinel. Callers should use errors.Is rather than parsing
// the human-readable error string.
var ErrVoiceoverDestinationUnavailable = errors.New(VoiceoverDestinationUnavailableCode)

// VoiceoverDestinationMismatchCode is the stable machine-readable code
// emitted when Drive confirms an uploaded file is not parented by the
// folder resolved for the voiceover plan. This is a hard failure; no
// post-upload move/repair is permitted.
const VoiceoverDestinationMismatchCode = "VOICEOVER_DESTINATION_MISMATCH"

// Status values for VoiceoverItemResult now live in types.go as the
// typed `Status` enum (PR-VO-AUDIT-P01, June 2026). See:
//   - types.go::Status / StatusCompleted / StatusFailed / ...
//   - types.go::FailureCode / FailureTTS / FailureUpload / ...
//
// The untyped `const (StatusCompleted = "completed"; ...)` literal
// constants previously declared here were REMOVED in favour of the
// typed enum so the aggregate check
// `item.Status == StatusFailed` is exhaustive at compile time.
// The VoiceoverItemResult.Status field is now `Status` (typed)
// instead of plain `string`.

// GenerateVoiceoversResult is the canonical use case result.
type GenerateVoiceoversResult struct {
	// OK is the overall success aggregate: true iff every per-item
	// Status equals StatusCompleted.
	OK bool

	// RequestID is the per-batch identifier (build/buildRequestID
	// shape: vo_<timestamp>_<6-hex-suffix>, see types.go). Stable
	// across the per-language fan-out — same value is written into
	// every voiceovers row's request_id column for cross-language audit.
	RequestID string

	// TotalOutputs is the count of Languages requested (snapshot
	// at the use case entry; never changes after Execute).
	TotalOutputs int

	// SuccessCount is the count of per-item Status == StatusCompleted.
	SuccessCount int

	// FailedCount is the count of per-item Status == StatusFailed.
	// OK is true iff FailedCount == 0.
	FailedCount int

	// PerLanguage is the per-language detail. Slot i corresponds
	// to cmd.Languages[i] — language-locked ordering is required so
	// a client iterating the result can correlate Language ↔ index
	// without consulting the input.
	PerLanguage []VoiceoverItemResult

	// StartedAt is the canonical UTC entry timestamp.
	StartedAt time.Time

	// CompletedAt is the canonical UTC exit timestamp (set even on
	// partial failure so audit tooling measures end-to-end latency).
	CompletedAt time.Time

	// Error is the global error (populated ONLY for cross-cutting
	// failures: validation, destination resolve, no-languages, etc.).
	// Empty when the per-item fan-out completed (regardless of overall
	// OK status — per-item errors live in PerLanguage[].Error).
	Error string

	// ErrorCode is the stable machine-readable code for a cross-cutting
	// failure. In particular, unavailable voiceover destinations must not
	// be represented as an unclassified generic error.
	ErrorCode string
}

// VoiceoverItemResult carries per-language detail correlated by language
// code (NOT by index — a safer correlation field for callers iterating
// the result without preserving the input index ordering).
type VoiceoverItemResult struct {
	// Language is the typed BCP-47 envelope (voiceover.Language)
	// per PR-VO-TYPED-PRIMITIVES — JSON wire shape is
	// byte-equivalent with the pre-refactor string field.
	Language Language

	// Voice is the BCP-47-tied voice name reported by TTSProvider
	// (empty if TTSProvider did not surface a voice field).
	Voice string

	// Status is the per-item terminal state: StatusCompleted | StatusFailed.
	// Typed (Status) so the aggregate use case check
	// `result.OK = (SuccessCount == TotalOutputs)` is exhaustive at
	// compile time — pre-P01 the literal `"completed"` / `"failed"`
	// allowed freeform string drift across stages, which is the
	// canonical audit P0.1 bug (see types.go::Status enum doc).
	Status Status

	// Error is populated when Status == StatusFailed. Empty otherwise.
	Error string

	// ErrorCode is the stable machine-readable failure code. It is
	// populated for destination integrity failures so job/API layers
	// do not need to parse Error text.
	ErrorCode string

	// DriveFileID is the canonical Drive file id surfaced after upload.
	DriveFileID string

	// DriveLink is the canonical Drive webViewLink surfaced after upload.
	DriveLink string

	// LocalPath is the path of the local audio file written by TTSProvider
	// (CleanedPath empty → LocalPath is the canonical artifact).
	LocalPath string

	// DurationMs is the measured TTS audio duration. It is propagated to
	// the script scene binding so API consumers do not need to probe the
	// local artifact again.
	DurationMs int64

	// CleanedPath is the path of the post-processed (silence removal)
	// audio file. Empty when RemoveSilence was false or post-process
	// failed before production.
	CleanedPath string

	// FileHash is the MD5 hex digest of the canonical artifact
	// (LocalPath or CleanedPath, whichever Lifecycle.ProcessAsset
	// fingerprints).
	FileHash string

	// Filename is the sanitised filename written to LocalPath/CleanedPath
	// and surfaced in the voiceovers.filename SQLite column.
	Filename string

	// DownloadLink is the canonical Drive download URL produced by
	// CanonicalDriveDownloadURL(fileID). Populated by the publish
	// stage (usecase.go::processOneLanguage +
	// process_voiceover_item.go::Execute) so downstream consumers
	// (scripts/jobs consumers, admin UI, retention sweeps) can
	// reach the file binary without re-deriving from DriveLink.
	DownloadLink string

	// SearchText is the semantic tagger's normalized search text. It is
	// populated when SemanticTagger is wired and remains empty when
	// semantic enrichment is unavailable.
	SearchText string

	// ID is the canonical voiceover row identifier (buildVoiceoverID
	// shape: vo_<sha256[:16]>) — same value as voiceovers.id column.
	ID string

	// StageProgress exposes the real per-language stage counters produced
	// by the child pipeline. It is serialized into child job results so
	// parent aggregators can merge translation, voiceover, upload, and
	// persistence outcomes without reconstructing them from percentages.
	StageProgress map[string]job.StageProgress

	// Timing carries the published timing bundle references (timing.json
	// SSOT + optional SRT/VTT projections). nil when the timing policy is
	// disabled (legacy behavior preserved byte-for-byte).
	Timing *VoiceoverTimingResult

	// SilenceCleanup is the observability summary of post-TTS silence
	// removal (original duration, leading/trailing trims, clean duration).
	// nil when RemoveSilence was false or no edits were reported.
	SilenceCleanup *SilenceCleanupReport `json:"silence_cleanup,omitempty"`
}

// VoiceoverTimingStatus is the canonical per-item timing bundle state.
// It distinguishes "timing exists and is accurate" from "timing is
// intentionally absent" so consumers never confuse an unavailable
// timing with a successful one (godlike/07 no-fake-availability).
type VoiceoverTimingStatus string

const (
	// TimingStatusCompleted — the canonical artifact and all requested
	// projections were built and published; links are real Publisher
	// file IDs, never hand-built.
	TimingStatusCompleted VoiceoverTimingStatus = "completed"
	// TimingStatusUnavailable — timing was requested but could not be
	// captured (provider returned no boundaries, or best-effort policy
	// combined with silence removal whose edit-map remap is not yet
	// implemented). No timestamps are fabricated.
	TimingStatusUnavailable VoiceoverTimingStatus = "unavailable"
	// TimingStatusFailed — timing was requested under the best-effort
	// policy but the bundle could not be built or published. The audio
	// stays completed; the timing failure is explicitly surfaced.
	TimingStatusFailed VoiceoverTimingStatus = "failed"
)

// VoiceoverTimingResult carries the published timing bundle references.
// JSON wire shape mirrors the design's VoiceoverTimingBinding surface:
// links are optional (only populated for the formats actually published)
// and hashes bind the artifact to exactly one synthesized text + one
// final audio file.
type VoiceoverTimingResult struct {
	Status VoiceoverTimingStatus `json:"status,omitempty"`

	JSONLink string `json:"json_link,omitempty"`
	SRTLink  string `json:"srt_link,omitempty"`
	VTTLink  string `json:"vtt_link,omitempty"`

	BoundaryMode string `json:"boundary_mode,omitempty"`

	WordCount  int   `json:"word_count,omitempty"`
	DurationUS int64 `json:"duration_us,omitempty"`

	TextSHA256  string `json:"text_sha256,omitempty"`
	AudioSHA256 string `json:"audio_sha256,omitempty"`

	// Moments are the deterministic timestamped projections of the
	// scene's semantic annotations (entity/keyword/phrase strings from the
	// LLM) onto the canonical word timing. The LLM never produced these
	// timestamps — they are derived exclusively via PhraseLocator, so a
	// moment exists only when its value occurs verbatim in the speech.
	// Empty when no annotation queries were supplied or none matched.
	Moments []audio.Moment `json:"moments,omitempty"`

	// Artifact is the full canonical SpeechTimingArtifact (version, provider,
	// boundary mode, hashes, duration, and the per-word timing array) from
	// which the summary fields above are projected. It is carried in-memory
	// only (json:"-") so downstream consumers that need the word-level
	// timing (e.g. the script runner's phrase→timestamp projection) receive
	// the SSOT verbatim without the wire result ballooning with a per-word
	// array. nil when no artifact was built (timing disabled / unavailable
	// / failed, or the zero-boundary best-effort degradation).
	Artifact *audio.SpeechTimingArtifact `json:"-"`
}
