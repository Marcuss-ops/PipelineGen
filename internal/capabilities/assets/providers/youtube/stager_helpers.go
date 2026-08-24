// Package youtube — stager_helpers.go (ART-002 P4.1, July 2026).
//
// Helper functions for YouTubeStager:
//
//   - timeNowUnixNano: a single point of indirection for time.Now().UnixNano()
//     so tests can swap it out for a deterministic clock if needed.
//   - parseDownloadSection: parses a yt-dlp-style "HH:MM:SS-HH:MM:SS"
//     DownloadSection string into SegmentStart/SegmentEnd time.Duration
//     values that the YouTube adapter's FetchRequest consumes.
package assets

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseDownloadSection parses a yt-dlp "HH:MM:SS-HH:MM:SS" string into
// start and end time.Durations. Empty input returns (0, 0, nil) which
// the underlying adapter treats as "full video".
//
// Format details:
//   - Both endpoints are "HH:MM:SS" with hours optional (e.g. "00:30" or
//     "00:01:30"). Mixing 2-component and 3-component endpoints is
//     rejected to keep the parser strict.
//   - The dash separator is the only accepted delimiter (yt-dlp's
//     canonical form). Whitespace around the dash is tolerated.
//
// Returns ErrYoutubeStagerInvalidSection (typed sentinel) wrapped with
// the offending input in the message.
func parseDownloadSection(s string) (time.Duration, time.Duration, error) {
	if s == "" {
		return 0, 0, nil
	}
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: %q (expected HH:MM:SS-HH:MM:SS)", ErrYoutubeStagerInvalidSection, s)
	}
	start, err := parseClock(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("%w: start=%q: %v", ErrYoutubeStagerInvalidSection, parts[0], err)
	}
	end, err := parseClock(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("%w: end=%q: %v", ErrYoutubeStagerInvalidSection, parts[1], err)
	}
	return start, end, nil
}

// parseClock parses "HH:MM:SS" or "MM:SS" into a time.Duration.
func parseClock(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	var h, m, sec int
	var err error
	switch len(parts) {
	case 2:
		if m, err = strconv.Atoi(parts[0]); err != nil {
			return 0, fmt.Errorf("parse minutes %q: %w", parts[0], err)
		}
		if sec, err = strconv.Atoi(parts[1]); err != nil {
			return 0, fmt.Errorf("parse seconds %q: %w", parts[1], err)
		}
	case 3:
		if h, err = strconv.Atoi(parts[0]); err != nil {
			return 0, fmt.Errorf("parse hours %q: %w", parts[0], err)
		}
		if m, err = strconv.Atoi(parts[1]); err != nil {
			return 0, fmt.Errorf("parse minutes %q: %w", parts[1], err)
		}
		if sec, err = strconv.Atoi(parts[2]); err != nil {
			return 0, fmt.Errorf("parse seconds %q: %w", parts[2], err)
		}
	default:
		return 0, fmt.Errorf("invalid clock format %q (expected HH:MM:SS or MM:SS)", s)
	}
	if h < 0 || m < 0 || sec < 0 {
		return 0, fmt.Errorf("negative component in %q", s)
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec)*time.Second, nil
}
