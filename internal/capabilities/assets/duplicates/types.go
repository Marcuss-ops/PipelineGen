// Package duplicates is the canonical capability for asset-level
// duplicate detection (godlike/06 "one owner per fact"; see
// architecture/deprecations.yaml#DRIVE-SEARCH-LEGACY-CONTRACT).
//
// Background. The historical `search.Candidate` carried
// LocalPath + DriveLink + Hash as runtime fields so the local
// search backend could surface "hash-source + duplicate-list"
// rows without a second DB lookup. Commit 3 CONTRACT
// (July 2026) lifts those fields off the universal search
// hit (they were strictly internal locators that the canonical
// candidate shape rigorously excluded per the QDRANT-004
// invariant "Nessun path locale o secret esposto") and
// declares a small dedicated DTO owned by THIS package.
//
// Capability seam. The duplicates capability is intentionally
// narrow: it exposes ONLY the typed DTO + the canonical Port
// (FindDuplicates). The legacy local-search-backend hash-
// match path (internal/capabilities/assets/search/dedup.go +
// localBackend.searchByHash) and the historical
// internal/application/assets/assetop/dedupe.go "asset-level
// dedupe-by-policy" are independent concerns and remain in their
// owning capability; this package does NOT subsume them.
//
// Submitter: the local search backend returns plain
// []search.Candidate and, on the side, []duplicates.DuplicateMatch
// when Query.Hash != "". The FindDuplicates handler reads the
// DTO directly. The composition root (internal/app/) wires the
// concrete duplicates.Repository implementation; today this is
// a thin in-memory stub; a SQLite-backed implementation is the
// forward-pointer target (architecture/current.yaml#DUPLICATES-
// SQLITE-PERSISTENCE, owner = assets, deadline 2026-08-31).
package duplicates

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// DuplicateMatch is one matched-duplicate row returned from the
// duplicates capability.
//
// The DTO carries three server-internal fields — LocalPath,
// DriveLink, Hash — that are NOT exposed on the universal
// `search.Candidate`. The dedicated DTO is justified because
// the FindDuplicates endpoint is an operator/admin surface
// (intentionally behind admin-only scope checks) for which the
// QDRANT-004 "no internal locator leak" invariant does not
// apply — operators SEE server-internal paths so they can
// deduplicate files at the storage layer. Public-facing
// /internal/media/* callers NEVER receive this DTO; the only
// HTTP route today that returns it is /internal/v1/assets/
// duplicates (admin-only, behind bearer + admin claim).
//
// Field rationale:
//   - AssetID: canonical asset UUID. Used as the cross-reference
//     key when the operator decides to keep / trash / merge.
//   - LocalPath: filesystem path on the running instance. The
//     operator uses this to locate the actual file for visual
//     verification or manual cleanup.
//   - DriveLink: signed-or-public google drive link to the same
//     asset (may differ LocalPath when the canonical copy lives
//     in Drive and the local copy is a cache).
//   - Hash: content hash, the deduplication key. By convention
//     MD5 of the canonical bytes; an MD5 collision between
//     visually-identical files is the duplicate-signal.
type DuplicateMatch struct {
	// AssetID is the canonical asset identifier (UUID).
	AssetID string `json:"asset_id"`

	// Source is the canonical source identity (e.g. "youtube",
	// "artlist", "stock", "local").
	Source string `json:"source"`

	// Name is the human-readable asset name.
	Name string `json:"name"`

	// ThumbnailURL is the public thumbnail URL, when available.
	ThumbnailURL string `json:"thumbnail_url,omitempty"`

	// LocalPath is the filesystem path to the asset's bytes on
	// the running instance. Used by operators for visual
	// verification or filesystem-level cleanup.
	LocalPath string `json:"local_path"`

	// DriveLink is the public-or-signed Google Drive link to the
	// same asset. Empty when the asset has no Drive presence.
	DriveLink string `json:"drive_link,omitempty"`

	// Hash is the content hash (MD5) used as the deduplication
	// key. Two assets with the same Hash are duplicates by
	// definition.
	Hash string `json:"hash"`
}
