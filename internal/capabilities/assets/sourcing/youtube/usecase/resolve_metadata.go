// Package usecase — ResolveClipMetadata is a pure-logic use case extracted from
// youtube/service.go::Register() (PR-CLIP-DECOM-1, July 2026).
//
// It owns the metadata-resolution steps (1, 2, and 5) of the legacy 14-step
// Register pipeline: URL sanitization, videoID extraction, timestamp validation,
// name/description/duration population.
//
// Per AGENTS.md Pattern 0 + Pattern 5: zero ports, zero I/O. The use case is a
// deterministic pure function that takes a command struct and returns a resolved
// metadata struct — fully testable without any mock.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the canonical
// owner of videoID extraction from a raw YouTube URL and of the name/description/
// duration fallback logic for the sourcing/youtube registration pipeline.
// The parent youtube package's helpers.go duplicates ExtractVideoIDFromURL and
// ExtractURLParam for backward compat until the CUTOVER phase (Wave B).
package assets

import (
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ResolveMetadataCommand carries every input needed to resolve clip metadata.
// It bundles the raw RegisterClipCommand fields with the post-fetch asset
// metadata so the use case can apply the canonical fallback chain without
// depending on any port.
type ResolveMetadataCommand struct {
	// From the HTTP request / RegisterClipCommand.
	URL         string
	Name        string
	Description string
	Source      string
	StartSec    float64
	EndSec      float64

	// From the FetchedAsset after provider.Fetch returns.
	FetchedName        string
	FetchedDescription string
	FetchedDuration    time.Duration
}

// ResolvedMetadata is the canonical output of the metadata-resolution step.
// Every downstream step in Register() (fetch, drive, db) reads from these fields.
type ResolvedMetadata struct {
	VideoID     string
	RawURL      string
	Name        string
	Description string
	DurationSec float64
	Duration    int
	Source      string
	StartSec    float64
	EndSec      float64
}

// ResolveClipMetadata resolves clip metadata from the raw command and
// post-fetch asset data. It is a pure function: deterministic, no side
// effects, no port dependencies.
//
// Resolution order (canonical per the legacy Register() pipeline):
//  1. Extract videoID from URL; fall back to UnixNano timestamp when URL
//     is not a recognisable YouTube link.
//  2. Rebuild rawURL as canonical https://www.youtube.com/watch?v=<videoID>
//     when the original URL did not carry the watch?v= prefix.
//  3. Parse ?start=N&end=N from the URL when StartSec/EndSec are zero.
//  4. Validate: start >= 0, end >= 0, start < end when end > 0.
//  5. Resolve name: cmd.Name → fetched.Name → videoID.
//  6. Resolve description: cmd.Description → fetched description (truncated
//     to 1000 chars) → empty.
//  7. Resolve duration: fetched.Duration → (endSec - startSec) → 0.
//  8. Validate: start < duration when duration > 0.
//  9. Default source to "youtube-manual" when empty.
func ResolveClipMetadata(cmd ResolveMetadataCommand) (*ResolvedMetadata, error) {
	// ── 1. Sanitize URL + extract video ID ─────────────────────────────
	rawURL := cmd.URL
	videoID := extractVideoID(rawURL)
	if videoID == "" {
		videoID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if videoID != "" && !strings.HasPrefix(rawURL, "https://www.youtube.com/watch?v="+videoID) {
		rawURL = "https://www.youtube.com/watch?v=" + videoID
	}

	startSec := cmd.StartSec
	if startSec == 0 {
		startSec = extractURLParam(rawURL, "start")
	}
	endSec := cmd.EndSec
	if endSec == 0 {
		endSec = extractURLParam(rawURL, "end")
	}

	// ── 2. Basic validation ────────────────────────────────────────────
	if endSec > 0 && startSec >= endSec {
		return nil, fmt.Errorf("invalid segment: start (%.1f) must be less than end (%.1f)", startSec, endSec)
	}
	if startSec < 0 || endSec < 0 {
		return nil, fmt.Errorf("start and end must be non-negative")
	}

	source := strings.TrimSpace(cmd.Source)
	if source == "" {
		source = "youtube-manual"
	}

	// ── 5. Populate metadata ───────────────────────────────────────────
	name := strings.TrimSpace(cmd.Name)
	if name == "" {
		name = cmd.FetchedName
	}
	if name == "" {
		name = videoID
	}

	description := strings.TrimSpace(cmd.Description)
	if description == "" {
		if d := cmd.FetchedDescription; d != "" {
			description = textutil.Truncate(d, 1000)
		}
	}

	durationSec := 0.0
	hasRealDuration := false
	if cmd.FetchedDuration > 0 {
		durationSec = cmd.FetchedDuration.Seconds()
		hasRealDuration = true
	} else if endSec > startSec {
		durationSec = endSec - startSec
	}
	duration := int(durationSec)

	// Only validate start vs. video duration when we have the REAL fetched
	// duration from the provider. When durationSec was derived from
	// (endSec - startSec), comparing start against it is meaningless —
	// it would always fail because start >= (end - start) for any clip
	// that starts past the midpoint of its own segment.
	if hasRealDuration && startSec > 0 && startSec >= durationSec {
		return nil, fmt.Errorf("start (%.1f) exceeds video duration (%.1f)", startSec, durationSec)
	}

	return &ResolvedMetadata{
		VideoID:     videoID,
		RawURL:      rawURL,
		Name:        name,
		Description: description,
		DurationSec: durationSec,
		Duration:    duration,
		Source:      source,
		StartSec:    startSec,
		EndSec:      endSec,
	}, nil
}

// extractVideoID pulls the YouTube video ID from a raw URL.
// Supports youtube.com/watch?v=ID and youtu.be/ID formats.
// Returns "" when the URL is not a recognisable YouTube link.
func extractVideoID(rawURL string) string {
	for _, part := range strings.Split(rawURL, "&") {
		if strings.HasPrefix(part, "v=") || strings.Contains(part, "?v=") {
			if idx := strings.Index(part, "v="); idx != -1 {
				id := part[idx+2:]
				if len(id) > 11 {
					id = id[:11]
				}
				return id
			}
		}
	}
	if idx := strings.LastIndex(rawURL, "youtu.be/"); idx != -1 {
		rest := rawURL[idx+len("youtu.be/"):]
		if end := strings.IndexAny(rest, "?&#"); end != -1 {
			rest = rest[:end]
		}
		return rest
	}
	return ""
}

// extractURLParam parses a numeric ?key=value (or &key=value) parameter
// from rawURL. Returns 0 when the param is absent or non-numeric.
func extractURLParam(rawURL, key string) float64 {
	prefixes := []string{"&" + key + "=", "?" + key + "="}
	for _, pfx := range prefixes {
		if idx := strings.Index(rawURL, pfx); idx != -1 {
			rest := rawURL[idx+len(pfx):]
			for i, c := range rest {
				if c == '&' || c == '?' || c == '#' {
					rest = rest[:i]
					break
				}
			}
			var v float64
			if _, err := fmt.Sscanf(rest, "%f", &v); err == nil {
				return v
			}
		}
	}
	return 0
}
