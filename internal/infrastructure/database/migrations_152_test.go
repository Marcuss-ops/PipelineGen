// Package storage — migrations_152_test.go holds the scenario tests
// for migration 152 (canonical metadata columns). PR-CATALOG-MULTILINGUA
// step 1 added 13 canonical metadata columns to media_assets to bring
// the schema in line with the broader cross-package SSOT contract.
//
// Covers:
//
//   - TestMigrations_152_CanonicalConsolidationColumnsPresent
//     PRAGMA table_info(media_assets) lists the 13 columns added
//     by migration 152 (source_provider, source_video_id,
//     source_channel_id, source_url, start_ms, end_ms,
//     original_language, title, binary_sha256, semantic_hash,	//     rights_status, policy_version). Mirrors
//     the structural pattern of the migration-099
//     QdrantAssetColumnsPresent test; each required entry MUST
//     be backed by a corresponding ALTER TABLE in
//     migrations/sqlite/152_add_canonical_metadata_columns.sql.
//
//     lifecycle_status was included in the original migration 152
//     but dropped in migration 230 (shadow/compatibility column —
//     lifecycle_state is the sole operational SSOT).
//
//   - TestMigrations_152_CanonicalConsolidationColumnsRoundTrip
//     Migrating an empty DB is the integration-test equivalent of
//     "FetchAsset works on fixture in-memory".
package storage

import (
	"testing"
)

func TestMigrations_152_CanonicalConsolidationColumnsPresent(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	required := []string{
		"source_provider", "source_video_id", "source_channel_id",
		"source_url", "start_ms", "end_ms", "original_language",
		"title", "binary_sha256", "semantic_hash",
		"rights_status", "policy_version",
	}
	seen := scanColumnNames(t, db, "media_assets")
	for _, col := range required {
		if _, ok := seen[col]; !ok {
			t.Errorf("media_assets missing canonical column %q (added by migration 152; canonical.go in this package must mirror it)", col)
		}
	}
}

func TestMigrations_152_CanonicalConsolidationColumnsRoundTrip(t *testing.T) {
	db, cleanup := applyFreshSmokeDB(t)
	defer cleanup()

	const assetID = "rt-canon-1"
	_, err := db.Exec(`INSERT INTO media_assets (
			id, lifecycle_state, source_provider, source_video_id, source_channel_id,
			source_url, start_ms, end_ms, original_language, title,
			binary_sha256, semantic_hash, rights_status,
			policy_version
		) VALUES (
			?, 'ACTIVE', 'youtube', 'yt-consolidate-1', 'UC_consolidate',
			'https://www.youtube.com/watch?v=yt-consolidate-1',
			?, ?, 'en', 'Round-Trip Canonical Title',
			?, 'semhash-from-asset-visual-summaries-0001',
			'permission_granted', 'v1'
		)`,
		assetID,
		int64(32000), int64(37000),
		// binary_sha256 is a 64-char SHA-256 hex string.
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	)
	if err != nil {
		t.Fatalf("insert canonical round-trip asset: %v", err)
	}
	var (
		sourceProvider, sourceVideoID, sourceChannelID, sourceURL string
		startMs, endMs                                            int64
		originalLanguage, title, binarySHA256, semanticHash       string
		rightsStatus, policyVersion                               string
	)
	err = db.QueryRow(`
		SELECT source_provider, source_video_id, source_channel_id,
		       source_url, start_ms, end_ms, original_language, title,
		       binary_sha256, semantic_hash, rights_status,
		       policy_version
		FROM media_assets WHERE id = ?`, assetID,
	).Scan(&sourceProvider, &sourceVideoID, &sourceChannelID, &sourceURL,
		&startMs, &endMs, &originalLanguage, &title,
		&binarySHA256, &semanticHash, &rightsStatus,
		&policyVersion)
	if err != nil {
		t.Fatalf("select canonical round-trip asset: %v", err)
	}
	expectations := map[string]string{
		"source_provider":   sourceProvider,
		"source_video_id":   sourceVideoID,
		"source_channel_id": sourceChannelID,
		"source_url":        sourceURL,
		"original_language": originalLanguage,
		"title":             title,
		"binary_sha256":     binarySHA256,
		"semantic_hash":     semanticHash,
		"rights_status":     rightsStatus,
		"policy_version":    policyVersion,
	}
	wants := map[string]string{
		"source_provider":   "youtube",
		"source_video_id":   "yt-consolidate-1",
		"source_channel_id": "UC_consolidate",
		"source_url":        "https://www.youtube.com/watch?v=yt-consolidate-1",
		"original_language": "en",
		"title":             "Round-Trip Canonical Title",
		"binary_sha256":     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"semantic_hash":     "semhash-from-asset-visual-summaries-0001",
		"rights_status":     "permission_granted",
		"policy_version":    "v1",
	}
	for col, got := range expectations {
		if got != wants[col] {
			t.Errorf("canonical round-trip %s = %q, want %q", col, got, wants[col])
		}
	}
	if startMs != 32000 {
		t.Errorf("canonical round-trip start_ms = %d, want 32000", startMs)
	}
	if endMs != 37000 {
		t.Errorf("canonical round-trip end_ms = %d, want 37000", endMs)
	}
}
