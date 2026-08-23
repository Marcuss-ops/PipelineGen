// Package youtube — helpers scoped exclusively to the YouTubeRegistrar use case.
//
// Per AGENTS.md Pattern 5 (one concept per file) + Pattern 0 (port
// abstraction): helpers live in the same package as their consumer so the
// source-code view stays narrow. These functions are pure (no port deps)
// and exported so they can be invoked from youtube.Service.Register
// without a cross-package reach-up into the parent sourcing package.
//
// P0-1 / commit 1 — moved out of internal/application/assets/sourcing/helpers.go
// as part of the use-case extraction. The parent sourcing/helpers.go now
// keeps only ScanLocalMp4 (which belongs to the LocalImporter use case,
// slated for commit 4 of P0-1).
package youtube

import (
	"fmt"
	"strings"

	domain "github.com/Marcuss-ops/PipelineGen/internal/capabilities/sourcing"
)

// ExtractVideoIDFromURL pulls the YouTube video ID from a raw URL.
// Supports youtube.com/watch?v=ID and youtu.be/ID formats. Returns "" when
// the URL is not a recognisable YouTube link.
func ExtractVideoIDFromURL(rawURL string) string {
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

// ExtractURLParam parses a numeric ?key=value (or &key=value) parameter
// from rawURL. Returns 0 when the param is absent or non-numeric.
func ExtractURLParam(rawURL, key string) float64 {
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

// BuildRelatedClipsQuery composes a provider-search query for related
// clips (best-effort, called only when EnrichmentPort.SearchRelated is wired).
func BuildRelatedClipsQuery(name, category string, tags []string) string {
	var parts []string
	if cat := strings.TrimSpace(category); cat != "" {
		parts = append(parts, cat)
	}
	maxTags := 2
	for _, t := range tags {
		if maxTags <= 0 {
			break
		}
		if tt := strings.TrimSpace(t); tt != "" {
			parts = append(parts, tt)
			maxTags--
		}
	}
	if n := strings.TrimSpace(name); n != "" {
		parts = append(parts, n)
	}
	return strings.Join(parts, " ")
}

// BuildDriveDescription composes the human-readable Drive file description
// that lands alongside the uploaded clip.
func BuildDriveDescription(name, reqDesc, fetchedDesc string, tags []string, category, source, url, videoID string) string {
	var parts []string
	if name != "" {
		parts = append(parts, "Name: "+name)
	}
	if reqDesc != "" {
		parts = append(parts, "Description: "+reqDesc)
	} else if fetchedDesc != "" {
		parts = append(parts, "Description: "+fetchedDesc)
	}
	if category != "" {
		parts = append(parts, "Category: "+category)
	}
	if source != "" {
		parts = append(parts, "Source: "+source)
	}
	if len(tags) > 0 {
		parts = append(parts, "Tags: "+strings.Join(tags, ", "))
	}
	if url != "" {
		parts = append(parts, "URL: "+url)
	}
	if videoID != "" {
		parts = append(parts, "VideoID: "+videoID)
	}
	return strings.Join(parts, "\n")
}

// CleanFolderName normalizes a Drive folder name for case-insensitive comparison
// (Drive's folder match is case-folded per Google Drive API).
func CleanFolderName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

// IndexStatus renders the indexing-status typed-enum for the
// RegisterClipResult. §12-5 EXPAND phase (godlike/06 SSOT migration):
// returns domain.SourcingIndexStatus — the legacy "enqueued"/"not_configured"
// placeholder strings have been retired in favor of the canonical
// 4-state lifecycle (architecture/issues.yaml#PR-CROSSPACKAGE-INDEXING-
// STATUS-§12-5).
//
// Wire mapping (was a wire breaking change in §12-5 EXPAND):
//
//	indexed=true  → domain.SourcingIndexStatusPending   ("pending")
//	indexed=false → domain.SourcingIndexStatusSkipped   ("skipped")
//
// The Pending/Skipped mapping preserves the pre-§12-5 intent:
// "enqueued" semantically means awaiting-in-flight (Pending),
// "not_configured" semantically means enrichment-bypass (Skipped).
//
// godlike/06 SSOT: this helper does NOT own the canonical enum —
// the canonical 4-state lifecycle + Validation + Marshal/Unmarshal
// contracts live in internal/domain/sourcing/index_status.go. The
// application-layer `sourcing.IndexingStatus` is a transparent Go
// type-alias to that enum; this helper writes the canonical typed
// value directly so the wire emission stays byte-stable.
func IndexStatus(indexed bool) domain.SourcingIndexStatus {
	if indexed {
		return domain.SourcingIndexStatusPending
	}
	return domain.SourcingIndexStatusSkipped
}
