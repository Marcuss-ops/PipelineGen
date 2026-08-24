// Package youtube — stager_errors.go (ART-002 P4.1, July 2026).
//
// Typed-error sentinels for the YouTubeStager adapter (godlike/07).
// All sentinels are declared as package-level vars reachable via
// errors.Is from any caller seam; the error messages carry the failing
// input via fmt.Errorf %w so log-scanners can correlate rejections
// with the SourceRef that triggered them.
package assets

import "errors"

// ErrYoutubeStagerInvalidSection is returned by parseDownloadSection
// when the input does not match the canonical yt-dlp "HH:MM:SS-HH:MM:SS"
// format. Distinct from ErrYoutubeStagerEmptyURL (which catches a
// missing URL entirely) so callers can branch on the failure mode
// without parsing the message.
var ErrYoutubeStagerInvalidSection = errors.New("youtube stager: invalid download section")
