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
// AssetID prefix heuristic. The classification rule is NOT duplicated here: it
// is capregistry.ResolveSourceObservations / ResolveContentObservations, shared
// with the SQLite registry adapter, so the two stores cannot disagree about
// what "one asset" or "multi-provenance" means. This file owns the PostgreSQL
// SELECT and the scan errors.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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
//
// This adapter owns ONLY the PostgreSQL SELECT and its scan errors: the
// classification (not found, ambiguity, which row wins) lives in
// capregistry.ResolveSourceObservations, the single implementation shared with
// the SQLite registry adapter.
func (r *PostgresCanonicalIdentityResolver) ResolveSource(ctx context.Context, sourceType, sourceRef string) (capregistry.CanonicalIdentity, error) {
	if r == nil || r.db == nil {
		return capregistry.CanonicalIdentity{}, errors.New("canonical identity resolver (postgres): not wired")
	}
	sourceType, sourceRef, err := capregistry.NormalizeSourceQuery(sourceType, sourceRef)
	if err != nil {
		return capregistry.CanonicalIdentity{}, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT asset_id, COALESCE(content_sha256, '') FROM media_asset_sources
		WHERE source_type = $1 AND source_uri = $2
		ORDER BY is_primary DESC, `+tsCastExpr("discovered_at")+` ASC`, sourceType, sourceRef)
	if err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): resolve source (%s, %s): %w", sourceType, sourceRef, err)
	}
	defer func() { _ = rows.Close() }()

	observations := make([]capregistry.SourceObservation, 0, 4)
	for rows.Next() {
		var observation capregistry.SourceObservation
		if err := rows.Scan(&observation.AssetID, &observation.ContentSHA256); err != nil {
			return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): scan source: %w", err)
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): iterate sources: %w", err)
	}
	return capregistry.ResolveSourceObservations(sourceType, sourceRef, observations)
}

// ResolveContent resolves contentSHA256 to its canonical identity. The
// multi-provenance rule (several assets → AssetID empty) is the shared one in
// capregistry.ResolveContentObservations.
func (r *PostgresCanonicalIdentityResolver) ResolveContent(ctx context.Context, contentSHA256 string) (capregistry.CanonicalIdentity, error) {
	if r == nil || r.db == nil {
		return capregistry.CanonicalIdentity{}, errors.New("canonical identity resolver (postgres): not wired")
	}
	contentSHA256, err := capregistry.NormalizeContentQuery(contentSHA256)
	if err != nil {
		return capregistry.CanonicalIdentity{}, err
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM media_assets
		WHERE content_sha256 = $1 AND content_sha256 != ''`, contentSHA256)
	if err != nil {
		return capregistry.CanonicalIdentity{}, fmt.Errorf("canonical identity resolver (postgres): resolve content %q: %w", contentSHA256, err)
	}
	defer func() { _ = rows.Close() }()

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
	return capregistry.ResolveContentObservations(contentSHA256, assetIDs)
}
