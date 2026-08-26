// Package youtube contains concrete YouTube-specific infrastructure adapters.
//
// Port contract: the application layer (internal/application/youtube) depends
// ONLY on the interfaces declared in its own ports.go. This package provides
// the concrete implementations: yt-dlp execution, subtitle parsing, metadata
// fetching, whisper transcription, and file management.
//
// Mapping to architectural rule (AGENTS.md Pattern 8):
//
//	application/youtube/** must NOT import database/sql, oauth sdk,
//	os/exec, internal/infrastructure/media/ffmpeg, or the concrete
//	downloader. Instead it imports these ports and the composition root
//	(internal/app/dependencies.go) wire the concrete adapters.
package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// TimedEntry represents a parsed subtitle cue. The values are in seconds.
type TimedEntry struct {
	Start float64
	End   float64
	Text  string
}

// LiveSearchResult is the raw shape of one yt-dlp --dump-json --flat-playlist
// search hit.
type LiveSearchResult struct {
	ID        string
	URL       string
	Title     string
	Duration  float64
	Uploader  string
	Thumbnail string
}

// VideoInfo / VideoThumbnail / VideoChapter are declared in ytdlp.go
// (pre-existing). Re-declaring them here would shadow the producer; keep
// ports.go thin.

// ProcessRunnerPort is the canonical subprocess wrapper. Production
// wires ProcessRunnerAdapter (process.go); tests may swap fakes.
type ProcessRunnerPort interface {
	Run(ctx context.Context, name string, args []string) (stdout, stderr string, err error)
}

// SubtitleFetcher downloads and parses the auto-generated + manual subtitle
// tracks for a YouTube video.
type SubtitleFetcher interface {
	FetchFullVTT(ctx context.Context, videoURL string) ([]TimedEntry, error)
	SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error
	// FetchSegmentSubtitles returns the canonical typed subtitle
	// track for [startSec, endSec] as an detail.ResolvedTextBundle
	// (plaintext + per-cue timings + detected language). This is the
	// typed surface used by TextTrackResolver.AcquireSegmentText
	// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.a, July 2026).
	//
	// Implementation contract: probe manual subtitles first (priority
	// 3 per the doc), fall back to auto-generated (priority 4). Empty
	// Cues + empty PlainText is a valid "no subtitles" signal (NOT an
	// error). Fetch and parse errors are typed (network, VTT
	// malformed) and propagated verbatim.
	FetchSegmentSubtitles(ctx context.Context, videoID string, startSec, endSec int) (*detail.ResolvedTextBundle, error)
}

// TranscriptResult is the canonical Whisper transcription output.
// The plain string-only return of the legacy TranscribeAudio method
// could not surface the detected language or the per-cue confidence,
// so a typed-result sibling method (TranscribeAudioWithDetection) is
// added to both the application-layer WhisperTranscriberPort and
// the infrastructure-layer WhisperTranscriber interface.
//
// godlike/06 SSOT (one canonical owner per fact): TranscriptResult
// is defined in internal/kernel/asset/transcript_result.go so the
// application-layer port and the infrastructure-layer port BOTH
// reference the same type without import cycles. The canonical
// contract is:
//   - Text: the canonical transcript plaintext (post-language-
//     detection). Empty when the model returned an empty
//     transcription (still a non-error signal — caller falls
//     through).
//   - DetectedLanguage: the BCP-47 language code Whisper detected
//     for the audio. The concrete adapter MUST normalize via the
//     canonical asset.Normalize helper and collapse unknown/empty
//     to "und" (BCP-47 undetermined) per godlike/07
//     no-fake-availability.
//   - Confidence: per-clip average probability [0.0, 1.0]; nil
//     when the underlying model does not report one.

// WhisperTranscriber is the local-transcription fallback when no official
// VTT subtitles are available.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b (July 2026): a typed-result
// sibling method (TranscribeAudioWithDetection) is added to surface
// DetectedLanguage + Confidence. The legacy plain-string
// TranscribeAudio method is RETAINED for back-compat with the
// Service.TranscribeAudio shim and the Step 10 metadata path; the
// canonical 5-level chain (TextTrackResolver.AcquireSegmentText
// priority 5) calls the new method so the resolver can apply the
// RequireLanguageCertainty policy gate.
type WhisperTranscriber interface {
	TranscribeAudio(ctx context.Context, localPath string) (string, error)
	// TranscribeAudioWithDetection returns the typed result
	// including the model's DetectedLanguage + Confidence.
	// Implementations MUST:
	//   - Normalize DetectedLanguage to BCP-47 (lang or lang-region).
	//   - Collapse unknown/empty DetectedLanguage to "und".
	//   - Return (detail.TranscriptResult{Text: ""}, nil) on empty
	//     transcription (NOT an error — caller falls through).
	//   - Return a typed error only on I/O / model failures.
	TranscribeAudioWithDetection(ctx context.Context, localPath string) (detail.TranscriptResult, error)
}

// ClipFiles is the on-disk media-file manager: writes metadata, writes
// transcripts, and removes stale files.
type ClipFiles interface {
	WriteMetadataFile(metaPath string, data []byte) error
	WriteTranscriptFile(transcriptPath string, data []byte) error
	RemoveIfStale(localPath string) error
}

// SearchRunner runs yt-dlp search/info CLI calls.
type SearchRunner interface {
	SearchLive(ctx context.Context, query string, limit int, sort string) ([]LiveSearchResult, error)
	GetVideoInfo(ctx context.Context, videoURL string) (VideoInfo, error)
}

// NOTE (June 2026): the previously-local MetadataFetcherPort was deleted.
// It was dead code — the production adapter `MetadataFetcherAdapter` already
// satisfies `youtubedto.VideoMetadataFetcherPort` (the application-side port
// declared in internal/capabilities/youtube/ports.go), not this local
// interface. Confirmed via repo-wide grep: zero external references.
// Callers that need the metadata-fetch capability depend on the
// application-side port, which has the canonical `*DownloaderMetadata` DTO.
// The deletion eliminates a parallel abstraction that would otherwise
// drift from the canonical shape.
