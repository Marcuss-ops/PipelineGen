// Package mediaregistry — content_link_backfill.go: SQLite implementation of
// the CAS content-link backfill (item 12 — backfill before the Qdrant v4
// rebuild).
//
// The media_asset_sources backfill (canonical_identity.go) reconstructs
// provenance; this backfill reconstructs the byte-identity half:
//
//  1. every non-empty media_assets.content_sha256 (and
//     media_asset_sources.content_sha256) gets a content_objects row, with the
//     canonical CAS address `cas://<sha256>` as storage_uri and
//     integrity_status = UNVERIFIED (size/bytes are not known from the digest
//     alone; the CAS integrity scanner verifies the blob later);
//  2. every media_asset_sources row whose owning asset already knows its bytes
//     but whose own content_sha256 is empty is linked to the asset's digest.
//
// godlike/07 fail-closed: digests are read from durable columns only. An asset
// without content_sha256 is content_sha_unknown (UNKNOWN), never fabricated
// from a provider ID or URL.
package mediaregistry

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// contentLinkAssetScope is the media_assets row scope shared by every query in
// this file: live assets that are not folders.
const contentLinkAssetScope = `lifecycle_state IN ('ACTIVE','PUBLISHED') AND COALESCE(media_type,'') != 'folder'`

// BackfillContentLinks reconstructs the CAS content links. apply=false
// performs the same classification/counting without mutating any row;
// apply=true writes the missing content_objects rows and source links inside
// one transaction.
func (r *CanonicalIdentityResolver) BackfillContentLinks(ctx context.Context, apply bool) (capregistry.ContentLinkBackfillReport, error) {
	report := capregistry.ContentLinkBackfillReport{}
	if r == nil || r.db == nil {
		return report, ErrNotWired
	}

	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE `+contentLinkAssetScope).Scan(&report.AssetsScanned); err != nil {
		return report, fmt.Errorf("content link backfill: count assets: %w", err)
	}
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE `+contentLinkAssetScope+` AND content_sha256 != ''`).Scan(&report.ContentSHAKnown); err != nil {
		return report, fmt.Errorf("content link backfill: count known content sha: %w", err)
	}
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets WHERE `+contentLinkAssetScope+` AND (content_sha256 = '' OR content_sha256 IS NULL)`).Scan(&report.ContentSHAUnknown); err != nil {
		return report, fmt.Errorf("content link backfill: count unknown content sha: %w", err)
	}
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM content_objects`).Scan(&report.ContentObjectsExisting); err != nil {
		return report, fmt.Errorf("content link backfill: count content objects: %w", err)
	}
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM media_assets a LEFT JOIN content_objects c ON c.sha256 = a.content_sha256 WHERE a.content_sha256 != '' AND c.sha256 IS NULL`).Scan(&report.BrokenCASLinks); err != nil {
		return report, fmt.Errorf("content link backfill: count broken cas links: %w", err)
	}

	var sourceLinksToFill int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_asset_sources s
		WHERE s.content_sha256 = ''
		  AND EXISTS (SELECT 1 FROM media_assets a WHERE a.id = s.asset_id AND a.content_sha256 != '')`).Scan(&sourceLinksToFill); err != nil {
		return report, fmt.Errorf("content link backfill: count source links: %w", err)
	}

	if !apply {
		// Preview: report the digests that WOULD get a content_object row.
		if err := r.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM (
				SELECT DISTINCT d.content_sha256
				FROM (
					SELECT content_sha256 FROM media_assets
					WHERE content_sha256 != '' AND `+contentLinkAssetScope+`
					UNION
					SELECT content_sha256 FROM media_asset_sources WHERE content_sha256 != ''
				) d
				LEFT JOIN content_objects c ON c.sha256 = d.content_sha256
				WHERE c.sha256 IS NULL
			)`).Scan(&report.ContentObjectsCreated); err != nil {
			return report, fmt.Errorf("content link backfill: preview content objects: %w", err)
		}
		report.SourceLinksBackfilled = sourceLinksToFill
		return report, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return report, fmt.Errorf("content link backfill: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	createdAssets, err := tx.ExecContext(ctx, `
		INSERT INTO content_objects (sha256, size_bytes, mime_type, storage_uri, created_at, integrity_status)
		SELECT DISTINCT content_sha256, 0, '', 'cas://' || content_sha256, ?, 'UNVERIFIED'
		FROM media_assets
		WHERE content_sha256 != '' AND `+contentLinkAssetScope+`
		ON CONFLICT(sha256) DO NOTHING`, now)
	if err != nil {
		return report, fmt.Errorf("content link backfill: insert asset content objects: %w", err)
	}
	createdSources, err := tx.ExecContext(ctx, `
		INSERT INTO content_objects (sha256, size_bytes, mime_type, storage_uri, created_at, integrity_status)
		SELECT DISTINCT content_sha256, 0, '', 'cas://' || content_sha256, ?, 'UNVERIFIED'
		FROM media_asset_sources
		WHERE content_sha256 != ''
		ON CONFLICT(sha256) DO NOTHING`, now)
	if err != nil {
		return report, fmt.Errorf("content link backfill: insert source content objects: %w", err)
	}
	nAssets, _ := createdAssets.RowsAffected()
	nSources, _ := createdSources.RowsAffected()
	report.ContentObjectsCreated = int(nAssets + nSources)

	linked, err := tx.ExecContext(ctx, `
		UPDATE media_asset_sources
		SET content_sha256 = (SELECT a.content_sha256 FROM media_assets a WHERE a.id = media_asset_sources.asset_id)
		WHERE content_sha256 = ''
		  AND EXISTS (SELECT 1 FROM media_assets a WHERE a.id = media_asset_sources.asset_id AND a.content_sha256 != '')`)
	if err != nil {
		return report, fmt.Errorf("content link backfill: link source content: %w", err)
	}
	nLinked, _ := linked.RowsAffected()
	report.SourceLinksBackfilled = int(nLinked)

	// Recompute broken CAS links post-apply: must be zero.
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_assets a LEFT JOIN content_objects c ON c.sha256 = a.content_sha256
		WHERE a.content_sha256 != '' AND c.sha256 IS NULL`).Scan(&report.BrokenCASLinks); err != nil {
		return report, fmt.Errorf("content link backfill: recount broken cas links: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("content link backfill: commit transaction: %w", err)
	}
	return report, nil
}

// BackfillContentSHA256 reconstructs the byte-identity half of the CAS
// registry: media_assets rows whose content_sha256 is empty/UNKNOWN but whose
// legacy file_hash is a valid 64-hex SHA-256 get content_sha256 (and its
// binary_sha256 projection) copied from file_hash.
//
// godlike/07 fail-closed: the guard is capregistry.IsSHA256Hex (64 hex
// chars), so a 32-char MD5 (clip_atomic_writer's empty-file fallback) or any
// other non-SHA-256 legacy value is skipped and left UNKNOWN — the digest is
// never fabricated from a Drive file ID, URL, or provider ID.
func (r *CanonicalIdentityResolver) BackfillContentSHA256(ctx context.Context, apply bool) (capregistry.ContentSHA256BackfillReport, error) {
	report := capregistry.ContentSHA256BackfillReport{}
	if r == nil || r.db == nil {
		return report, ErrNotWired
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, COALESCE(file_hash, '') FROM media_assets
		WHERE (content_sha256 = '' OR content_sha256 IS NULL OR content_sha256 = ?)
		  AND COALESCE(file_hash, '') != ''`, capregistry.ContentSHA256Unknown)
	if err != nil {
		return report, fmt.Errorf("content sha256 backfill: scan candidates: %w", err)
	}
	defer rows.Close()

	type candidate struct{ id, fileHash string }
	var items []candidate
	for rows.Next() {
		var it candidate
		if err := rows.Scan(&it.id, &it.fileHash); err != nil {
			return report, fmt.Errorf("content sha256 backfill: scan row: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return report, fmt.Errorf("content sha256 backfill: iterate candidates: %w", err)
	}
	report.CandidatesScanned = len(items)

	var tx *sql.Tx
	if apply {
		tx, err = r.db.BeginTx(ctx, nil)
		if err != nil {
			return report, fmt.Errorf("content sha256 backfill: begin transaction: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
	}
	var writer execer = r.db
	if tx != nil {
		writer = tx
	}

	for _, it := range items {
		if !capregistry.IsSHA256Hex(it.fileHash) {
			report.SkippedNonSHA256++
			continue
		}
		if apply {
			if _, err := writer.ExecContext(ctx,
				`UPDATE media_assets SET content_sha256 = ?, binary_sha256 = ? WHERE id = ?`,
				it.fileHash, it.fileHash, it.id); err != nil {
				return report, fmt.Errorf("content sha256 backfill: update %q: %w", it.id, err)
			}
		}
		report.Backfilled++
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return report, fmt.Errorf("content sha256 backfill: commit: %w", err)
		}
	}
	return report, nil
}
