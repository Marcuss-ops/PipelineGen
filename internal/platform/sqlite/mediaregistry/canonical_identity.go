// Package mediaregistry — canonical_identity.go: SQLite adapter for the
// canonical identity resolver + expand/backfill/cutover tooling.
//
// ResolveSource / ResolveContent read durable registry facts
// (media_asset_sources, media_assets.content_sha256) — never the AssetID
// prefix. Backfill reconstructs media_asset_sources rows from the canonical
// media_assets columns (source_video_id for youtube, drive_file_id for
// drive) and reports the fail-closed UNKNOWN set instead of guessing.
package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// CanonicalIdentityResolver is the SQLite implementation of
// capregistry.CanonicalIdentityResolver + capregistry.CanonicalIdentityBackfiller.
type CanonicalIdentityResolver struct {
	db *sql.DB
}

// NewCanonicalIdentityResolver constructs the adapter.
func NewCanonicalIdentityResolver(db *sql.DB) (*CanonicalIdentityResolver, error) {
	if db == nil {
		return nil, errors.New("canonical identity resolver: nil database")
	}
	return &CanonicalIdentityResolver{db: db}, nil
}

var (
	_ capregistry.CanonicalIdentityResolver   = (*CanonicalIdentityResolver)(nil)
	_ capregistry.CanonicalIdentityBackfiller = (*CanonicalIdentityResolver)(nil)
)

// ResolveSource resolves (sourceType, sourceRef) to the canonical asset.
func (r *CanonicalIdentityResolver) ResolveSource(ctx context.Context, sourceType, sourceRef string) (capregistry.CanonicalIdentity, error) {
	if r == nil || r.db == nil {
		return capregistry.CanonicalIdentity{}, ErrNotWired
	}
	sourceType = strings.TrimSpace(sourceType)
	sourceRef = strings.TrimSpace(sourceRef)
	if sourceType == "" || sourceRef == "" {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("%w: source_type and source_ref are required", capregistry.ErrAssetSourceInvalid)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, content_sha256 FROM media_asset_sources
		WHERE source_type = ? AND source_uri = ?
		ORDER BY is_primary DESC, discovered_at ASC`, sourceType, sourceRef)
	if err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver: resolve source (%s, %s): %w", sourceType, sourceRef, err)
	}
	defer rows.Close()

	var (
		identity capregistry.CanonicalIdentity
		seen     = false
	)
	assetIDs := make(map[string]struct{})
	for rows.Next() {
		var assetID, contentSHA string
		if err := rows.Scan(&assetID, &contentSHA); err != nil {
			return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver: scan source: %w", err)
		}
		assetIDs[assetID] = struct{}{}
		identity = capregistry.CanonicalIdentity{
			AssetID:       assetID,
			SourceType:    sourceType,
			SourceRef:     sourceRef,
			ContentSHA256: contentSHA,
		}
		seen = true
	}
	if err := rows.Err(); err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver: iterate sources: %w", err)
	}
	if !seen {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("%w: source (%s, %s)", capregistry.ErrCanonicalIdentityNotFound, sourceType, sourceRef)
	}
	if len(assetIDs) > 1 {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("%w: source (%s, %s) resolves to %d assets", capregistry.ErrCanonicalIdentityAmbiguous, sourceType, sourceRef, len(assetIDs))
	}
	return identity, nil
}

// ResolveContent resolves contentSHA256 to its canonical identity. When
// exactly one asset references the bytes, AssetID is populated; when the
// bytes are multi-provenance, AssetID is left empty.
func (r *CanonicalIdentityResolver) ResolveContent(ctx context.Context, contentSHA256 string) (capregistry.CanonicalIdentity, error) {
	if r == nil || r.db == nil {
		return capregistry.CanonicalIdentity{}, ErrNotWired
	}
	contentSHA256 = strings.TrimSpace(contentSHA256)
	if contentSHA256 == "" {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("%w: content_sha256 is required", capregistry.ErrContentObjectInvalid)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE content_sha256 = ? AND content_sha256 != ''`, contentSHA256)
	if err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver: resolve content %q: %w", contentSHA256, err)
	}
	defer rows.Close()

	var assetIDs []string
	for rows.Next() {
		var assetID string
		if err := rows.Scan(&assetID); err != nil {
			return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver: scan content: %w", err)
		}
		assetIDs = append(assetIDs, assetID)
	}
	if err := rows.Err(); err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver: iterate content: %w", err)
	}
	if len(assetIDs) == 0 {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("%w: content %s", capregistry.ErrCanonicalIdentityNotFound, contentSHA256)
	}
	identity := capregistry.CanonicalIdentity{ContentSHA256: contentSHA256}
	if len(assetIDs) == 1 {
		identity.AssetID = assetIDs[0]
	}
	return identity, nil
}

// Backfill reconstructs media_asset_sources rows from the canonical
// media_assets columns and returns the expand/backfill/cutover report.
func (r *CanonicalIdentityResolver) Backfill(ctx context.Context) (capregistry.BackfillReport, error) {
	return r.backfill(ctx, true)
}

// PreviewBackfill computes the same report as Backfill without changing any
// rows. It is intentionally concrete (not part of the runtime capability
// port): administrative tooling may use it to require an explicit --apply
// before the expand/backfill step mutates production state.
func (r *CanonicalIdentityResolver) PreviewBackfill(ctx context.Context) (capregistry.BackfillReport, error) {
	return r.backfill(ctx, false)
}

func (r *CanonicalIdentityResolver) backfill(ctx context.Context, apply bool) (capregistry.BackfillReport, error) {
	if r == nil || r.db == nil {
		return capregistry.BackfillReport{}, ErrNotWired
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, source, source_video_id, drive_file_id, source_url, content_sha256
		FROM media_assets
		WHERE lifecycle_state IN ('ACTIVE','PUBLISHED') AND COALESCE(media_type, '') != 'folder'`)
	if err != nil {
		return capregistry.BackfillReport{}, fmt.Errorf("canonical identity backfill: scan assets: %w", err)
	}
	defer rows.Close()

	nowStr := time.Now().UTC().Format(time.RFC3339)
	report := capregistry.BackfillReport{}

	type row struct {
		assetID, source, videoID, driveID, sourceURL, contentSHA string
	}
	var items []row
	for rows.Next() {
		var it row
		if err := rows.Scan(&it.assetID, &it.source, &it.videoID, &it.driveID, &it.sourceURL, &it.contentSHA); err != nil {
			return capregistry.BackfillReport{}, fmt.Errorf("canonical identity backfill: scan asset: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return capregistry.BackfillReport{}, fmt.Errorf("canonical identity backfill: iterate assets: %w", err)
	}

	report.AssetsTotal = len(items)
	for _, it := range items {
		if strings.TrimSpace(it.contentSHA) != "" {
			report.ContentSHAKnown++
		} else {
			report.ContentSHAUnknown++
		}

		sourceType, sourceRef := deriveSourceIdentity(it.source, it.videoID, it.driveID, it.sourceURL)
		if sourceRef == "" {
			report.SourcesUnknown++
			continue
		}

		sourceID := capregistry.DeriveAssetSourceID(it.assetID, sourceType, sourceRef, "")
		already, err := r.sourceExists(ctx, it.assetID, sourceType, sourceRef)
		if err != nil {
			return capregistry.BackfillReport{}, err
		}
		if already {
			report.SourcesAlreadyKnown++
			continue
		}
		if apply {
			if err := registerSource(ctx, r.db, capregistry.AssetSource{
				SourceID:      sourceID,
				AssetID:       it.assetID,
				ContentSHA256: it.contentSHA,
				SourceType:    sourceType,
				SourceURI:     sourceRef,
				DiscoveredAt:  nowStr,
				IsPrimary:     true,
			}); err != nil {
				return capregistry.BackfillReport{}, fmt.Errorf("canonical identity backfill: register source %q: %w", it.assetID, err)
			}
		}
		report.SourcesBackfilled++
	}

	report.DuplicateSourceIdentity, err = r.countDuplicateSourceIdentity(ctx)
	if err != nil {
		return capregistry.BackfillReport{}, err
	}
	report.DuplicateContentSHA, err = r.countDuplicateContentSHA(ctx)
	if err != nil {
		return capregistry.BackfillReport{}, err
	}
	return report, nil
}

// deriveSourceIdentity maps only explicit durable source columns. It does not
// inspect AssetID or parse provider-specific prefixes; unsupported records are
// returned as UNKNOWN and remain visible in the backfill report.
func deriveSourceIdentity(source, sourceVideoID, driveFileID, sourceURL string) (string, string) {
	source = strings.ToLower(strings.TrimSpace(source))
	switch source {
	case capregistry.SourceIdentityYouTube:
		return capregistry.SourceIdentityYouTube, strings.TrimSpace(sourceVideoID)
	case capregistry.SourceIdentityDrive, "clip_drive", "voiceover":
		return capregistry.SourceIdentityDrive, strings.TrimSpace(driveFileID)
	case capregistry.SourceIdentityArtlist:
		return capregistry.SourceIdentityArtlist, strings.TrimSpace(sourceURL)
	case "stock":
		ref := strings.TrimSpace(sourceVideoID)
		if ref == "" {
			ref = strings.TrimSpace(sourceURL)
		}
		return "stock", ref
	case "internet_images":
		return capregistry.SourceIdentityURL, strings.TrimSpace(sourceURL)
	case "created", "script", "document":
		return "manual", strings.TrimSpace(driveFileID)
	default:
		if ref := strings.TrimSpace(driveFileID); ref != "" {
			return "manual", ref
		}
		return "", ""
	}
}

// BackfillTaxonomy migrates only rows with missing canonical dimensions. The
// mapping is based on durable source/media_type columns, never on AssetID
// formatting. apply=false performs the same classification without writes.
func (r *CanonicalIdentityResolver) BackfillTaxonomy(ctx context.Context, apply bool) (capregistry.TaxonomyBackfillReport, error) {
	if r == nil || r.db == nil {
		return capregistry.TaxonomyBackfillReport{}, ErrNotWired
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, source, media_type, filename
		FROM media_assets
		WHERE lifecycle_state IN ('ACTIVE','PUBLISHED')
		  AND (COALESCE(namespace,'')='' OR COALESCE(asset_kind,'')='' OR COALESCE(source_type,'' )='')`)
	if err != nil {
		return capregistry.TaxonomyBackfillReport{}, fmt.Errorf("taxonomy backfill: scan assets: %w", err)
	}
	defer rows.Close()
	report := capregistry.TaxonomyBackfillReport{}
	for rows.Next() {
		var id, source, mediaType, filename string
		if err := rows.Scan(&id, &source, &mediaType, &filename); err != nil {
			return capregistry.TaxonomyBackfillReport{}, fmt.Errorf("taxonomy backfill: scan row: %w", err)
		}
		report.AssetsConsidered++
		tax, replacementType, ok := legacyTaxonomy(id, source, mediaType, filename)
		if !ok {
			report.TaxonomyUnknown++
			continue
		}
		if apply {
			query := `UPDATE media_assets SET namespace=?, asset_kind=?, source_type=?`
			args := []any{tax.Namespace, tax.AssetKind, tax.SourceType}
			if replacementType != "" {
				query += `, media_type=?`
				args = append(args, replacementType)
			}
			query += `, updated_at=datetime('now') WHERE id=?`
			args = append(args, id)
			if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
				return capregistry.TaxonomyBackfillReport{}, fmt.Errorf("taxonomy backfill: update %q: %w", id, err)
			}
		}
		report.TaxonomyBackfilled++
	}
	if err := rows.Err(); err != nil {
		return capregistry.TaxonomyBackfillReport{}, fmt.Errorf("taxonomy backfill: iterate: %w", err)
	}
	return report, nil
}

func legacyTaxonomy(id, source, mediaType, filename string) (capregistry.AssetTaxonomy, string, bool) {
	source = strings.ToLower(strings.TrimSpace(source))
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	tax := capregistry.AssetTaxonomy{AssetID: id, SourceType: "manual"}
	switch {
	case source == "voiceover" && mediaType == "audio":
		tax.Namespace, tax.MediaType, tax.AssetKind, tax.SourceType = "audio", capregistry.MediaAudio, capregistry.AssetVoiceover, capregistry.SourceIdentityDrive
	case source == "created" && mediaType == "audio":
		tax.Namespace, tax.MediaType, tax.AssetKind, tax.SourceType = "audio", capregistry.MediaAudio, capregistry.AssetVoiceover, "pipelinegen"
	case (source == "created" || source == "script") && mediaType == "text":
		tax.Namespace, tax.MediaType, tax.AssetKind, tax.SourceType = "text", capregistry.MediaText, capregistry.AssetMetadata, "pipelinegen"
	case source == "stock" && mediaType == "metadata":
		tax.Namespace, tax.MediaType, tax.AssetKind, tax.SourceType = "metadata", capregistry.MediaText, capregistry.AssetMetadata, "stock"
	case (source == "created" || source == "document") && mediaType == "document":
		tax.Namespace, tax.MediaType, tax.AssetKind, tax.SourceType = "outputs", capregistry.MediaDocument, capregistry.AssetDocument, "pipelinegen"
	default:
		return capregistry.AssetTaxonomy{}, "", false
	}
	if err := tax.Validate(); err != nil {
		return capregistry.AssetTaxonomy{}, "", false
	}
	if mediaType == "metadata" {
		return tax, string(capregistry.MediaText), true
	}
	_ = filename
	return tax, "", true
}

func (r *CanonicalIdentityResolver) sourceExists(ctx context.Context, assetID, sourceType, sourceRef string) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, `
		SELECT 1 FROM media_asset_sources
		WHERE asset_id = ? AND source_type = ? AND source_uri = ?`,
		assetID, sourceType, sourceRef).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("canonical identity backfill: check source existence: %w", err)
	}
	return true, nil
}

func (r *CanonicalIdentityResolver) countDuplicateSourceIdentity(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT source_type, source_uri FROM media_asset_sources
			GROUP BY source_type, source_uri
			HAVING COUNT(DISTINCT asset_id) > 1
		)`).Scan(&n); err != nil {
		return 0, fmt.Errorf("canonical identity backfill: duplicate source identity: %w", err)
	}
	return n, nil
}

func (r *CanonicalIdentityResolver) countDuplicateContentSHA(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT content_sha256 FROM media_assets
			WHERE content_sha256 != ''
			GROUP BY content_sha256
			HAVING COUNT(DISTINCT id) > 1
		)`).Scan(&n); err != nil {
		return 0, fmt.Errorf("canonical identity backfill: duplicate content sha: %w", err)
	}
	return n, nil
}
