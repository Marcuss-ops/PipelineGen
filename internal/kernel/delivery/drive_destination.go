// Package delivery — canonical Drive destination resolver (PR-CANONICAL-DRIVE-DESTINATION, July 2026).
//
// DriveDestinationInput is the unified semantic input that ALL pipelines
// (YouTube, Stock, Voiceover, Artlist) pass to ResolveDriveDestination
// instead of independently deriving RootFolderName + PathLeafName +
// AssetLocationInput fields. The resolver is a pure function (no
// database, network, or config dependencies) that maps raw pipeline
// inputs to the canonical Drive layout.
//
// godlike/06 SSOT (one canonical owner per fact): ResolveDriveDestination
// is the SOLE owner of the derivation logic that produces DriveDestination.
// Existing per-pipeline helpers (stockRootFolderName, perClipLeafName,
// deriveNormalizedGroup) continue working as-is; callers that adopt the
// resolver will deprecate their local derivation over time via the
// EXPAND→BACKFILL→CUTOVER→CONTRACT migration sequence.
//
// godlike/07 minimum-blast-radius: this file is additive-only. No
// existing signatures change; no existing callers break. The resolver
// sits ABOVE the existing BuildPublishRequest mapper — callers first
// resolve DriveDestination, then feed LocationInput into BuildPublishRequest
// and RootFolderName/PathLeafName into VerifiedArtifact.
package delivery

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/slug"
)

// ── Input types ──────────────────────────────────────────────────────

// DriveDestinationInput unifies all pipeline-specific raw inputs needed
// to derive both the semantic location (AssetLocationInput) and the
// physical Drive artifact fields (RootFolderName, PathLeafName).
//
// Each pipeline populates only the fields it owns:
//
//   - YouTube: Category, Subject (=videoID), ClipTitle, ClipStartSec,
//     ClipEndSec. RunFolderName is typically empty (Drive root resolved
//     by DestinationRegistry).
//
//   - Stock: Category, Subject (=slug/title), Provider, ClipSlug,
//     ClipTitle, ClipStartSec, ClipEndSec, RunFolderName,
//     RunSubfolder, RunSearchQueries, RunDirectURLs, RunContextTime.
//
//   - Voiceover: Project, Language. Clip/Run fields typically empty.
//
//   - Artlist: Category, Subject (=search term). Provider = "artlist".
//
// godlike/07 NO-FAKE-AVAILABILITY: empty fields are NOT backfilled by
// the resolver — each field's zero value propagates through so downstream
// callers see the honest absence and can probe per their own fallback
// chain (e.g. YouTubeClipPath's Group→Category→"youtube_uncategorized").
type DriveDestinationInput struct {
	// ── Canonical semantic fields (→ AssetLocationInput) ───────────
	Category string // Logical taxonomy bucket (e.g. "Boxe", "Boxing")
	Subject  string // Identity within group (e.g. videoID, slug)
	Name     string // Human-friendly alternative to Subject
	Style    string // Visual style tag for Image destinations
	Provider string // Upstream source (e.g. "youtube", "pexels", "artlist")
	Project  string // Project identifier (voiceover/book/script)
	Language string // BCP-47 tag for per-language subfoldering

	// ── Explicit path overrides ────────────────────────────────────
	// ParentFolderID is the legacy admin escape hatch for
	// VerifiedArtifact.ParentFolderID. When non-empty, the
	// resolver passes it through verbatim. New callers SHOULD NOT
	// set this — use Category/Subject/Provider instead.
	ParentFolderID string

	// ── Run-level root context (Stock pipeline) ────────────────────
	// These fields drive the RootFolderName derivation cascade:
	//  1. RunFolderName (operator-supplied, highest priority)
	//  2. RunSubfolder (operator-supplied secondary)
	//  3. RunSearchQueries[0] (legacy search-only)
	//  4. RunDirectURLs[0] (legacy direct-url)
	//  5. "stock_<YYYY-MM-DD>" from RunContextTime (universal fallback)
	RunFolderName    string
	RunSubfolder     string
	RunSearchQueries []string
	RunDirectURLs    []string
	RunContextTime   time.Time // deterministic date for fallback; zero = no date fallback

	// ── Clip-level leaf context (Stock / YouTube) ──────────────────
	// These fields drive the PathLeafName derivation cascade:
	//  1. ClipSlug (operator-supplied, highest priority)
	//  2. slug.SlugifyTitle(ClipTitle)
	//  3. "HH-MM-SS_to_HH-MM-SS" from ClipStartSec/ClipEndSec
	ClipSlug     string
	ClipTitle    string
	ClipStartSec float64
	ClipEndSec   float64
}

// ── Output type ──────────────────────────────────────────────────────

// DriveDestination is the resolved canonical Drive location emitted by
// ResolveDriveDestination. It bundles:
//
//  1. LocationInput — the pre-mapped AssetLocationInput for
//     BuildPublishRequest (the canonical mapper).
//
//  2. RootFolderName — for VerifiedArtifact.RootFolderName (human-readable
//     top-level folder, e.g. "Pacquiao_Vs_Broner").
//
//  3. PathLeafName — for VerifiedArtifact.PathLeafName (immediate leaf
//     folder, e.g. "round-7-broner-barcolla").
//
//  4. ParentFolderID — for VerifiedArtifact.ParentFolderID (legacy
//     admin escape hatch; empty for normal operation).
//
// godlike/06 SSOT: DriveDestination is the canonical bridge between the
// domain-layer resolver and the infrastructure-layer VerifiedArtifact.
// Callers MUST NOT independently derive these fields when they have a
// DriveDestination available.
type DriveDestination struct {
	// LocationInput is the pre-mapped semantic location for
	// BuildPublishRequest. The caller passes this as the Location
	// field of AssetPublishInput.
	LocationInput AssetLocationInput

	// RootFolderName is the human-readable top-level Drive folder name.
	// Used by VerifiedArtifact.RootFolderName and stockArtifactPathParts.
	RootFolderName string

	// PathLeafName is the immediate leaf folder name for the artifact's
	// Drive subfolder. Used by VerifiedArtifact.PathLeafName.
	PathLeafName string

	// ParentFolderID is the explicit Drive root folder ID (legacy
	// admin escape hatch). Empty for normal operation. Used by
	// VerifiedArtifact.ParentFolderID.
	ParentFolderID string
}

// ── Resolver function ────────────────────────────────────────────────

// ResolveDriveDestination is the canonical pure-function resolver that
// maps pipeline-specific raw inputs to a unified DriveDestination.
//
// This is the SOLE owner of the derivation logic (godlike/06 SSOT):
// callers that adopt the resolver will deprecate their local
// RootFolderName/PathLeafName/Group derivation over time.
//
// The function is deterministic: same inputs → same output across
// retries, parallel agents, and cross-session recovery.
//
// godlike/07 NO-FAKE-AVAILABILITY: zero-value fields propagate through
// without silent backfill. Downstream callers (YouTubeClipPath,
// BuildPublishRequest) apply their own per-destination fallback chains.
func ResolveDriveDestination(in DriveDestinationInput) DriveDestination {
	loc := AssetLocationInput{
		Category: strings.TrimSpace(in.Category),
		Subject:  strings.TrimSpace(in.Subject),
		Name:     strings.TrimSpace(in.Name),
		Style:    strings.TrimSpace(in.Style),
		Provider: strings.TrimSpace(in.Provider),
		Project:  strings.TrimSpace(in.Project),
		Language: strings.TrimSpace(in.Language),
	}

	return DriveDestination{
		LocationInput:  loc,
		RootFolderName: deriveRootFolderName(in),
		PathLeafName:   derivePathLeafName(in),
		ParentFolderID: strings.TrimSpace(in.ParentFolderID),
	}
}

// ── Internal derivation helpers ──────────────────────────────────────

// deriveRootFolderName implements the canonical RootFolderName cascade
// (migrated from stockRootFolderName in step_publish.go):
//
//  1. RunFolderName — operator-supplied readable name (highest priority)
//  2. RunSubfolder — operator-supplied secondary root
//  3. RunSearchQueries[0] — sanitized first query (legacy search-only)
//  4. RunDirectURLs[0] — basename of first URL (legacy direct-url)
//  5. "stock_<YYYY-MM-DD>" — universal date fallback from RunContextTime
//
// Returns "" when all inputs are empty (caller applies its own default).
func deriveRootFolderName(in DriveDestinationInput) string {
	if n := sanitizedFolderName(in.RunFolderName); n != "" {
		return n
	}
	if n := sanitizedFolderName(in.RunSubfolder); n != "" {
		return n
	}
	if n := firstSanitizedQuery(in.RunSearchQueries); n != "" {
		return n
	}
	if n := firstSanitizedURLBasename(in.RunDirectURLs); n != "" {
		return n
	}
	if !in.RunContextTime.IsZero() {
		return "stock_" + in.RunContextTime.UTC().Format("2006-01-02")
	}
	return ""
}

// derivePathLeafName implements the canonical PathLeafName cascade
// (migrated from perClipLeafName in step_publish.go):
//
//  1. ClipSlug — operator-supplied explicit slug (highest priority)
//  2. slug.SlugifyTitle(ClipTitle) — title-derived slug
//  3. "HH-MM-SS_to_HH-MM-SS" — start-end literal (universal fallback)
//
// Returns "" when all inputs are empty (caller applies its own default).
func derivePathLeafName(in DriveDestinationInput) string {
	// 1. Explicit slug (highest priority)
	if raw := strings.TrimSpace(in.ClipSlug); raw != "" {
		if safe := pathutil.SafeFolderName(raw); safe != "" && safe != "untitled" && containsAlphanumeric(safe) {
			return safe
		}
	}
	// 2. Title-derived slug
	if title := strings.TrimSpace(in.ClipTitle); title != "" {
		s := slug.SlugifyTitle(title)
		if s != "" && s != "untitled" {
			return s
		}
	}
	// 3. Start-end literal (universal fallback)
	if in.ClipStartSec > 0 || in.ClipEndSec > 0 {
		return formatTimeRange(in.ClipStartSec, in.ClipEndSec)
	}
	return ""
}

// ── Shared sanitization helpers ──────────────────────────────────────
//
// godlike/06 SSOT (one canonical owner per fact): these helpers are
// the SOLE canonical implementations. Previous mirror copies in
// stockpipeline/step_publish.go have been retired (PR-CANONICAL-CLIP-NAMING,
// July 2026) — callers import the domain delivery package instead.

// sanitizedFolderName trims whitespace, returns "" for empty input,
// otherwise runs through pathutil.SafeFolderName. Empty-in / all-
// whitespace-in collapses to "" so the caller continues to the next
// fallback rule.
func sanitizedFolderName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return pathutil.SafeFolderName(s)
}

// FirstSanitizedQuery is the exported entry point for the canonical
// first-non-empty-sanitized-query helper.
func FirstSanitizedQuery(queries []string) string { return firstSanitizedQuery(queries) }

// firstSanitizedQuery returns the first non-empty sanitized search
// query from queries. Returns "" if none survive.
func firstSanitizedQuery(queries []string) string {
	for _, q := range queries {
		if name := sanitizedFolderName(q); name != "" {
			return name
		}
	}
	return ""
}

// FirstSanitizedURLBasename is the exported entry point for the canonical
// first-non-empty-sanitized-URL-basename helper.
func FirstSanitizedURLBasename(urls []string) string { return firstSanitizedURLBasename(urls) }

// firstSanitizedURLBasename returns the first non-empty sanitized
// URL basename (sans file extension) from urls. Returns "" if none survive.
func firstSanitizedURLBasename(urls []string) string {
	for _, u := range urls {
		if name := sanitizedURLBasename(u); name != "" {
			return name
		}
	}
	return ""
}

// SanitizedURLBasename is the exported entry point for the canonical
// URL-basename sanitization helper.
func SanitizedURLBasename(rawURL string) string { return sanitizedURLBasename(rawURL) }

// sanitizedURLBasename strips query + fragment layers from a raw URL,
// takes the URL path's Base, strips the file extension, then runs
// through sanitizedFolderName. Returns "" on empty / malformed input.
func sanitizedURLBasename(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if parsed, err := url.Parse(s); err == nil && parsed.Path != "" {
		s = parsed.Path
	}
	base := filepath.Base(s)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return sanitizedFolderName(base)
}

// formatTimeRange formats start/end seconds as "HH-MM-SS_to_HH-MM-SS".
// Mirrors the stockpipeline perClipLeafName fallback literal exactly.
// Fractional seconds are truncated (int cast): 32.7s → 00-00-32, same as 32.1s.
// This is intentional — Drive folder names are human-readable, not precise.
func formatTimeRange(startSec, endSec float64) string {
	s, e := int(startSec), int(endSec)
	return fmt.Sprintf("%02d-%02d-%02d_to_%02d-%02d-%02d",
		s/3600, (s%3600)/60, s%60,
		e/3600, (e%3600)/60, e%60,
	)
}

// ContainsAlphanumeric is the exported entry point for the canonical
// alphanumeric-content check helper.
func ContainsAlphanumeric(s string) bool { return containsAlphanumeric(s) }

// containsAlphanumeric returns true if s contains at least one letter
// or digit. Used to reject slugs that sanitize to pure punctuation
// (e.g. "___", "---", "...").
func containsAlphanumeric(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
