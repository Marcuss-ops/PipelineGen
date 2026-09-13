// Package media — canonical_identity.go: the PostgreSQL implementation of the
// canonical identity resolver.
//
// MEDIA-SSOT (September 2026): identity is a MEDIA fact
// (media_asset_sources + media_assets.content_sha256), so it must be read from
// the PostgreSQL media SSOT — the same database the committer writes. The
// former resolver read the operational SQLite registry, which meant provider
// discovery canonicalised against a different database than the one holding
// the assets it was deduplicating against. That read split-brain is the
// reason this adapter exists.
//
// ResolveSource / ResolveContent read durable registry facts ONLY — never the
// AssetID prefix heuristic. This mirrors
// internal/platform/sqlite/mediaregistry/canonical_identity.go
// statement-for-statement, translated to PostgreSQL placeholders.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// PostgresCanonicalIdentityResolver is the PostgreSQL implementation of
// capregistry.CanonicalIdentityResolver.
type PostgresCanonicalIdentityResolver struct {
	db *sql.DB
}

// NewPostgresCanonicalIdentityResolver constructs the adapter. Fail-fast: a
// nil database is a programmer error, not a silent no-op (godlike/07).
func NewPostgresCanonicalIdentityResolver(db *sql.DB) (*PostgresCanonicalIdentityResolver, error) {
	if db == nil {
		return nil, errors.New("canonical identity resolver (postgres): nil database")
	}
	return &PostgresCanonicalIdentityResolver{db: db}, nil
}

var _ capregistry.CanonicalIdentityResolver = (*PostgresCanonicalIdentityResolver)(nil)

// ResolveSource resolves (sourceType, sourceRef) to the canonical asset.
func (r *PostgresCanonicalIdentityResolver) ResolveSource(ctx context.Context, sourceType, sourceRef string) (capregistry.CanonicalIdentity, error) {
	if r == nil || r.db == nil {
		return capregistry.CanonicalIdentity{}, errors.New("canonical identity resolver (postgres): not wired")
	}
	sourceType = strings.TrimSpace(sourceType)
	sourceRef = strings.TrimSpace(sourceRef)
	if sourceType == "" || sourceRef == "" {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("%w: source_type and source_ref are required", capregistry.ErrAssetSourceInvalid)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, COALESCE(content_sha256, '') FROM media_asset_sources
		WHERE source_type = $1 AND source_uri = $2
		ORDER BY is_primary DESC, discovered_at ASC`, sourceType, sourceRef)
	if err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): resolve source (%s, %s): %w", sourceType, sourceRef, err)
	}
	defer rows.Close()

	var (
		identity capregistry.CanonicalIdentity
		seen     bool
	)
	assetIDs := make(map[string]struct{})
	for rows.Next() {
		var assetID, contentSHA string
		if err := rows.Scan(&assetID, &contentSHA); err != nil {
			return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): scan source: %w", err)
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
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): iterate sources: %w", err)
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
// bytes are multi-provenance (several assets, one content object), AssetID is
// left empty and the caller treats the identity as a content object.
func (r *PostgresCanonicalIdentityResolver) ResolveContent(ctx context.Context, contentSHA256 string) (capregistry.CanonicalIdentity, error) {
	if r == nil || r.db == nil {
		return capregistry.CanonicalIdentity{}, errors.New("canonical identity resolver (postgres): not wired")
	}
	contentSHA256 = strings.TrimSpace(contentSHA256)
	if contentSHA256 == "" {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("%w: content_sha256 is required", capregistry.ErrContentObjectInvalid)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE content_sha256 = $1 AND content_sha256 != ''`, contentSHA256)
	if err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): resolve content %q: %w", contentSHA256, err)
	}
	defer rows.Close()

	var assetIDs []string
	for rows.Next() {
		var assetID string
		if err := rows.Scan(&assetID); err != nil {
			return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): scan content: %w", err)
		}
		assetIDs = append(assetIDs, assetID)
	}
	if err := rows.Err(); err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): iterate content: %w", err)
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
