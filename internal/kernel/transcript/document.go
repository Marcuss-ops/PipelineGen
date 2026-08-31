// Package transcript — leaf domain type moved out of transcripts/
// in Commit G (June 2026) to break the import cycle
//
//	monitor  →  transcripts  (port interface returns transcript.Document)
//	transcripts  →  monitor  (compile-time assertion for TranscriptProvider)
//
// The leaf placement is canonical for shared domain types per
// godlike/06 §"Database and config ownership": a type owned by
// multiple packages lives in a leaf package with no side-imports.
// Both `internal/capabilities/assets/monitor/ports.go` and
// `internal/capabilities/transcripts/*` import this package.
//
// Field-level invariants preserved verbatim from the original
// location:
//   - VideoID is the canonical YouTube identifier (matches
//     downloader.VideoInfo.ID and youtube_discoveries.video_id).
//   - Language is the BCP-47 code the subtitles were fetched for.
//   - Source is the canonical provider-tag ("asr" for auto-captions,
//     "manual" for user-uploaded subtitles).
//   - Text is the concatenated plain-text body, capped at 8000 chars.
//   - DurationSec is the end-of-last-entry timestamp in seconds.
//   - Entries preserves the timed cue entries so the analyser can
//     re-emit them as [MM:SS] markers in the LLM prompt without
//     re-downloading the VTT file.
package transcript

import (
	"fmt"
	"strings"
	"time"
)

// Document is the canonical structured transcript shape returned by
// TranscriptProvider.Fetch. Subsumes the legacy GetTranscript +
// GetTimedTranscript pair (no double-fetch in the orchestrator).
type Document struct {
	// VideoID is the canonical YouTube ID (downloader.VideoInfo.ID).
	VideoID string `json:"video_id"`

	// Language is the BCP-47 code the subtitles were fetched for
	// (e.g. "en", "it"). Empty string means "unspecified; default".
	Language string `json:"language,omitempty"`

	// Source is the canonical provider tag.
	Source string `json:"source,omitempty"`

	// Text is the concatenated plain-text body (≤ 8KB).
	Text string `json:"text"`

	// DurationSec is end-of-document timestamp (drives the 60s window
	// chunker in AnalyzeFull).
	DurationSec float64 `json:"duration_sec,omitempty"`

	// Entries preserves the timed cue entries (Start, End, Text).
	Entries []Entry `json:"entries,omitempty"`

	// FetchedAt is the UTC RFC3339 timestamp of the provider return.
	// L2 SQLite cache relies on row-level cached_at for TTL derivation;
	// FetchedAt is for observability.
	FetchedAt time.Time `json:"fetched_at,omitempty"`
}

// Entry is the timed cue entry shape. Mirrors the original
// transcripts.TranscriptEntry field-by-field.
type Entry struct {
	// Start is start timestamp in seconds (float for sub-second
	// precision; pre-Step-9 segment_finder.go used float64 too).
	Start float64
	// End is end timestamp in seconds.
	End float64
	// Text is the cleaned subtitle text (no XML tags, no markers).
	Text string
}

// CacheKey composes the canonical cache lookup key. Format:
//
//	videoID + ":" + language + ":" + source
//
// (lowercased to match the artlist cache's lowerKey convention).
// The source tag participates in the key because ASR vs manual
// subtitles can yield materially different text; caching them as
// one entity would be a silent-fidelity regression.
func CacheKey(videoID, language, source string) string {
	return lowerKey(strings.Join([]string{
		strings.TrimSpace(videoID),
		strings.TrimSpace(language),
		strings.TrimSpace(source),
	}, ":"))
}

// lowerKey fast-lower-cases ASCII A-Z bytes (per the existing artlist
// cache::lowerKey convention). Deliberately NOT strings.ToLower
// because the canonical key segments are all ASCII; the inline
// transform avoids the stdlib allocation for hot L1 lookups.
func lowerKey(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// String renders the canonical fingerprint for logging + grep audits.
func (d Document) String() string {
	return fmt.Sprintf("transcript.Document{videoID=%q language=%q source=%q duration=%.1fs entries=%d text=%dchars}",
		d.VideoID, d.Language, d.Source, d.DurationSec, len(d.Entries), len(d.Text))
}
