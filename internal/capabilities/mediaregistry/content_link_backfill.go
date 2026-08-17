// Package mediaregistry — content_link_backfill.go: contract for the CAS
// content-link backfill (item 12 — backfill before the Qdrant v4 rebuild).
//
// The media_asset_sources backfill reconstructs provenance; the content-link
// backfill reconstructs the byte-identity half: every non-empty
// media_assets.content_sha256 (and media_asset_sources.content_sha256) must
// resolve to a content_objects row, and every provenance source whose owning
// asset already knows its bytes must carry that digest. Both halves must be
// complete BEFORE the Qdrant reindex so the projection is built over a
// consistent registry.
//
// godlike/07 fail-closed: the backfill never invents a digest. Digests are
// read from the durable columns only; an asset without content_sha256 is
// counted content_sha_unknown and left UNKNOWN (never fabricated from a
// provider ID / URL).
package mediaregistry

// ContentLinkBackfillReport describes the CAS content-link backfill outcome.
// broken_cas_links must be zero after a successful apply: every non-empty
// content_sha256 resolves to a content_objects row.
type ContentLinkBackfillReport struct {
	// AssetsScanned is the number of media_assets rows in scope
	// (ACTIVE/PUBLISHED, non-folder).
	AssetsScanned int `json:"assets_scanned"`

	// ContentSHAKnown is the number of in-scope assets with a non-empty
	// content_sha256.
	ContentSHAKnown int `json:"content_sha_known"`

	// ContentSHAUnknown is the number of in-scope assets with an empty
	// content_sha256 (fail-closed UNKNOWN, never guessed).
	ContentSHAUnknown int `json:"content_sha_unknown"`

	// ContentObjectsCreated is the number of content_objects rows created
	// by this backfill (0 in preview mode counts what WOULD be created).
	ContentObjectsCreated int `json:"content_objects_created"`

	// ContentObjectsExisting is the number of content_objects rows present
	// BEFORE this backfill ran.
	ContentObjectsExisting int `json:"content_objects_existing"`

	// SourceLinksBackfilled is the number of media_asset_sources rows whose
	// empty content_sha256 was filled from the owning asset's digest.
	SourceLinksBackfilled int `json:"source_links_backfilled"`

	// BrokenCASLinks is the number of assets with a non-empty content_sha256
	// that does not resolve to a content_objects row. After a successful
	// apply this must be zero.
	BrokenCASLinks int `json:"broken_cas_links"`
}

// ContentSHA256BackfillReport describes the byte-identity reconstruction
// outcome: media_assets rows whose content_sha256 is empty/UNKNOWN but whose
// legacy file_hash is a valid 64-hex SHA-256 get the digest copied from
// file_hash (plus its binary_sha256 projection).
//
// godlike/07 fail-closed: a file_hash that is NOT a 64-hex SHA-256 (e.g. a
// 32-char MD5 from the clip_atomic_writer empty-file fallback, or any other
// unknown legacy value) is skipped and left UNKNOWN — never fabricated.
type ContentSHA256BackfillReport struct {
	// CandidatesScanned is the number of rows with an empty/UNKNOWN
	// content_sha256 and a non-empty file_hash.
	CandidatesScanned int `json:"candidates_scanned"`

	// Backfilled is the number of rows whose file_hash passed the 64-hex
	// SHA-256 guard and was copied into content_sha256 (and binary_sha256).
	Backfilled int `json:"backfilled"`

	// SkippedNonSHA256 is the number of rows whose file_hash was NOT a
	// 64-hex SHA-256 (MD5 or unknown) and was left UNKNOWN.
	SkippedNonSHA256 int `json:"skipped_non_sha256"`
}
