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

import "context"

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
}

// WhisperTranscriber is the local-transcription fallback when no official
// VTT subtitles are available.
type WhisperTranscriber interface {
	TranscribeAudio(ctx context.Context, localPath string) (string, error)
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

// MetadataFetcherPort is the local interface for the metadata adapter.
// Production wires MetadataFetcherAdapter (metadata.go); tests can swap
// in a fake to avoid invoking yt-dlp.
type MetadataFetcherPort interface {
	GetVideoMetadata(ctx context.Context, videoURL string) (*YouTubeMetadata, error)
}
