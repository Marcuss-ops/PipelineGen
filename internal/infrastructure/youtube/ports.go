// Package youtube contains concrete YouTube-specific infrastructure adapters.
//
// Port contract: the application layer (internal/application/youtube) depends
// ONLY on the interfaces declared in its own ports.go. This package provides
// the concrete implementations: yt-dlp execution, subtitle parsing, metadata
// fetching, whisper transcription, and file management.
package youtube

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
