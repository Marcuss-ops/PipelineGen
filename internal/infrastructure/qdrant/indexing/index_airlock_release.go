// Package indexing — index_airlock_release.go: AssetData → IndexDocument airlock
// — RELEASE (canonicalisation auxiliary) phase.
//
// Split rationale (acquire/commit/release), see index_airlock.go header:
//
//   - This file owns the `release` phase in the airlock's phase metaphor.
//
// Release here is NOT a resource-cleanup release (no lock-unlock, no
// goroutine drain, no fd close). The airlock owns no scavengeable
// surface. Within this code surface, `release` is the canonicalisation
// pass that runs AFTER raw fields have been read from the source-of-
// truth (acquire) and BEFORE the wire-ready doc is committed (commit).
// It polishes the raw values into their canonical-for-airlock form:
// trimmed, deduped, URL-parsed, drive-path-slashed, metadata-bools
// extracted, ints fallback-resolved.
//
// Cross-file deps (same package `indexing`, accessed without explicit
// imports from acquire / commit):
//   - This file declares helpers; functions in this file are called by
//     index_airlock.go (acquire) during the doc build. They are NOT
//     called from index_airlock_commit.go (commit) — commit reaches
//     straight into validateDenseVector + populate EmbeddingArtifact.
//
// Splitting rationale for keeping them out of acquire directly: the
// acquire phase would balloon to ~450 LOC if these helpers lived
// inline; the release phase keeps the doc-builder readable by giving
// each canonicalisation a named function.
package indexing

import (
	"path/filepath"
	"strconv"
	"strings"
)

// buildSemanticTitle composes the canonical semantic_title fallback
// when the top-level AssetData title is empty AND the metadata bag
// doesn't carry an explicit semantic_title. Returns a
// deduped-trimmed-joined string.
func buildSemanticTitle(title, event string, round int, scene, subject string) string {
	if title != "" {
		return strings.Join(dedupTrimmedStrings(title), " ")
	}
	parts := make([]string, 0, 4)
	if event != "" && event != title {
		parts = append(parts, event)
	}
	if round > 0 {
		parts = append(parts, "round "+strconv.Itoa(round))
	}
	if scene != "" && scene != title {
		parts = append(parts, scene)
	}
	if subject != "" && subject != title {
		parts = append(parts, subject)
	}
	return strings.Join(dedupTrimmedStrings(parts...), " ")
}

// dedupTrimmedStrings returns the unique-with-trim equivalent of the
// input variadic. Comparison key is lowercased so that "Pacquiao" and
// "pacquiao" collapse; the FIRST-seen casing wins in the output.
//
// godlike/06 SSOT: this is the canonical dedup helper for the airlock
// string-pass (SemanticTitle, EmbeddingText participations, lists).
func dedupTrimmedStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

// mergeStringSlices is the multi-input counterpart of
// dedupTrimmedStrings. Lowercase-keyed dedup, trimmed, preserves
// first-seen casing. nil inputs are tolerated (canonical empty-slice
// output when all inputs are empty).
func mergeStringSlices(slices ...[]string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, slice := range slices {
		for _, v := range slice {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			key := strings.ToLower(v)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

// parseSourceVideoID extracts the canonical 11-char video ID from a
// YouTube URL, falling back to the caller-supplied fallback when the
// URL is missing, malformed, or not YouTube-shaped. The fallback is
// always returned trimmed.
//
// godlike/06 SSOT: this is the airlock's canonical video-ID extractor.
// Callers that emit video IDs to the wire MUST go through this helper
// so the canonical `v=…` / last-path-segment / fallback order is
// preserved across all producers (godlike/07 fail-closed: a malformed
// non-YouTube URL emits the caller-supplied fallback only, never an
// empty string unless fallback was empty).
func parseSourceVideoID(sourceURL, fallback string) string {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return strings.TrimSpace(fallback)
	}
	if strings.Contains(sourceURL, "youtube.com") || strings.Contains(sourceURL, "youtu.be") {
		if idx := strings.LastIndex(sourceURL, "v="); idx >= 0 {
			id := sourceURL[idx+2:]
			if amp := strings.IndexByte(id, '&'); amp >= 0 {
				id = id[:amp]
			}
			id = strings.TrimSpace(id)
			if id != "" {
				return id
			}
		}
		if idx := strings.LastIndex(sourceURL, "/"); idx >= 0 && idx < len(sourceURL)-1 {
			id := sourceURL[idx+1:]
			if q := strings.IndexByte(id, '?'); q >= 0 {
				id = id[:q]
			}
			id = strings.TrimSpace(id)
			if id != "" {
				return id
			}
		}
	}
	return strings.TrimSpace(fallback)
}

// cleanDrivePath converts an OS-native path to forward-slash form
// (canonical on Drive — both Windows-shaped and POSIX-shaped inputs
// collapse to the same wire value). Empty returns empty.
func cleanDrivePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(path)
}

// metadataBool reads a boolean value from the metadata map. Returns
// false when the key is absent OR the value is not a boolean (godlike/07
// NO-FAKE-AVAILABILITY: never returns nil-interface and never
// type-asserts silently).
func metadataBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	v, ok := meta[key]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return b && ok
}

// metadataBoolPtr returns a *bool canonicalising the "has_dialogue"
// field through a two-source precedence (top-level AssetData first,
// then metadata_json fallback). Returns nil when neither source has a
// definitive value (godlike/07 wire-shape invariant: a missing
// tri-state value is omitted at wire time, NOT collapsed to false).
func metadataBoolPtr(meta map[string]any, top *bool) *bool {
	if top != nil {
		return top
	}
	if meta == nil {
		return nil
	}
	v, ok := meta["has_dialogue"].(bool)
	if !ok {
		return nil
	}
	return &v
}

// intOrFallback returns the top-level AssetData field if non-zero,
// otherwise the MetadataJSON-derived fallback (godlike/06 SSOT
// airlock precedence contract for int fields). The canonical
// firstNonEmpty helper (in payload_builder.go) handles the same
// contract for strings; the int counterpart uses 0 as the "not set"
// sentinel.
//
// Caveat (forward-pointer PR-ASSETDATA-INT-SENTINEL): for int
// fields where 0 is a legitimate value (e.g. ChunkIndex=0 for the
// first chunk), a caller that explicitly sets top-level=0 and also
// has a stale Metadata entry will get the Metadata value. Acceptable
// for the current state; a future PR can swap to a pointer-based
// sentinel if the conflict becomes real.
//
// Lives in the release phase (auxiliary canonicalisation) rather than
// inline in acquire because the caveat about the 0-sentinel is shared
// by all int fallback callsites and is easier to maintain in one place.
func intOrFallback(top, fallback int) int {
	if top != 0 {
		return top
	}
	return fallback
}
