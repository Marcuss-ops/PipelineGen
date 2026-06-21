// Package youtube contains concrete YouTube-specific infrastructure adapters.
//
// Port contract: the application layer (internal/application/youtube) depends
// ONLY on the interfaces declared here. The concrete adapters live in this
// package and perform the OS-level exec, file IO, and CLI coordination
// that the application layer is forbidden from doing directly.
//
// Mapping to architectural rule (AGENTS.md Pattern 8):
//   application/youtube/** must NOT import database/sql, oauth sdk,
//   os/exec, internal/infrastructure/media/ffmpeg, or the concrete
//   downloader. Instead it imports these ports and the composition root
//   (internal/app/dependencies.go) wire the concrete adapters.
package youtube

import (
	"context"
)

// TimedEntry represents a parsed subtitle cue. The values are in seconds.
type TimedEntry struct {
	Start float64
	End   float64
	Text  string
}

// LiveSearchResult is the raw shape of one yt-dlp --dump-json --flat-playlist
// search hit. The application layer converts it into asset.Asset for storage.
type LiveSearchResult struct {
	ID        string
	URL       string
	Title     string
	Duration  float64
	Uploader  string
	Thumbnail string
}

// SubtitleFetcher downloads and parses the auto-generated + manual subtitle
// tracks for a YouTube video. The application layer uses this to:
//   1. Slice subtitles for a clip window (sliceSubtitles in application).
//   2. Pull the full VTT transcript for segment-discovery heuristics.
type SubtitleFetcher interface {
	// FetchFullVTT downloads the VTT file for videoURL and parses it into
	// a list of timed entries. Returns (entries, nil) when successful.
	FetchFullVTT(ctx context.Context, videoURL string) ([]TimedEntry, error)

	// SliceSubtitles writes a transcript text file at outputPath with only
	// the cues overlapping [startSec, endSec]. It uses the canonical
	// YouTube auto-sub format with the rolling-cue dedup algorithm.
	SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error
}

// WhisperTranscriber is the local-transcription fallback when no official
// VTT subtitles are available. The default implementation shells out to
// scripts/tools/transcribe_detect_lang.py via python3.
type WhisperTranscriber interface {
	// TranscribeAudio runs Whisper on localPath, returns the transcript
	// text. Implementations should honour context cancellation.
	TranscribeAudio(ctx context.Context, localPath string) (string, error)
}

// ClipFiles is the on-disk media-file manager: writes metadata, writes
// transcripts, and removes stale files. The application-layer
// cache strategy policy stays in the application; this port only
// performs the actual IO.
type ClipFiles interface {
	// WriteMetadataFile writes the JSON descriptor for a clip.
	WriteMetadataFile(metaPath string, data []byte) error
	// WriteTranscriptFile writes the .txt transcript beside the clip path.
	WriteTranscriptFile(transcriptPath string, data []byte) error
	// RemoveIfStale deletes a local file if it exists. Errors are
	// tolerated by callers that only need a "best effort" cleanup.
	RemoveIfStale(localPath string) error
}

// SearchRunner runs yt-dlp search/info CLI calls. Distinct from the generic
// downloader.Downloader so future provider substitution (e.g. invidious
// or piped.video) can keep the same contract.
type SearchRunner interface {
	// SearchLive runs yt-dlp search (ytsearchN:query or
	// /results?search_query=...&sp=CAM%253D for views-sorted hits).
	SearchLive(ctx context.Context, query string, limit int, sort string) ([]LiveSearchResult, error)
	// GetVideoInfo runs yt-dlp --dump-json --no-playlist to fetch the
	// full video metadata (used for chapter extraction, description,
	// tags, view count, etc.).
	GetVideoInfo(ctx context.Context, videoURL string) (VideoInfo, error)
}
