// Package mediaregistry — source_identity.go: SQLite adapter for the
// source identity registry. Implements capregistry.SourceIdentityStore
// against the source_identity_registry table created by
// migrations/sqlite/198_source_identity_registry.sql.
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure path returns a typed
// error or the nil-return convention documented on the port. A nil
// database surfaces ErrSourceIdentityNotWired instead of a silent success.
package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// ErrSourceIdentityNotWired is returned when the store is constructed with
// a nil *sql.DB (composition error, not a runtime availability event).
var ErrSourceIdentityNotWired = errors.New("source identity sqlite adapter: not wired")

// SourceIdentityStore is the SQLite implementation of the source identity
// registry port.
type SourceIdentityStore struct {
	db *sql.DB
}

// NewSourceIdentityStore constructs the adapter. Fail-fast: a nil database
// is a programmer error, not a silent no-op (godlike/07).
func NewSourceIdentityStore(db *sql.DB) (*SourceIdentityStore, error) {
	if db == nil {
		return nil, errors.New("source identity sqlite adapter: nil database")
	}
	return &SourceIdentityStore{db: db}, nil
}

// Compile-time pin: *SourceIdentityStore satisfies the port.
var _ capregistry.SourceIdentityStore = (*SourceIdentityStore)(nil)

const sourceIdentityColumns = `source_type, source_key, content_sha256, source_version, discovered_at, last_seen_at, verification_status`

// Lookup returns the identity for (sourceType, sourceKey), or (nil, nil)
// when the mapping is unknown.
func (s *SourceIdentityStore) Lookup(ctx context.Context, sourceType, sourceKey string) (*capregistry.SourceIdentity, error) {
	if s == nil || s.db == nil {
		return nil, ErrSourceIdentityNotWired
	}
	if strings.TrimSpace(sourceType) == "" || strings.TrimSpace(sourceKey) == "" {
		return nil, fmt.Errorf("%w: source_type and source_key are required", capregistry.ErrSourceIdentityInvalid)
	}
	var (
		id              capregistry.SourceIdentity
		versionRaw      sql.NullString
		verificationRaw sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT `+sourceIdentityColumns+`
		FROM source_identity_registry
		WHERE source_type = ? AND source_key = ?`, sourceType, sourceKey).Scan(
		&id.SourceType, &id.SourceKey, &id.ContentSHA256, &versionRaw,
		&id.DiscoveredAt, &id.LastSeenAt, &verificationRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("source identity sqlite adapter: lookup (%s, %s): %w", sourceType, sourceKey, err)
	}
	if versionRaw.Valid {
		id.SourceVersion = versionRaw.String
	}
	if verificationRaw.Valid {
		id.VerificationStatus = verificationRaw.String
	}
	return &id, nil
}

// Record upserts the identity mapping. Idempotent on (source_type,
// source_key): re-recording refreshes content_sha256, source_version,
// last_seen_at and verification_status without duplicating the row.
func (s *SourceIdentityStore) Record(ctx context.Context, id capregistry.SourceIdentity) error {
	if s == nil || s.db == nil {
		return ErrSourceIdentityNotWired
	}
	if err := validateSourceIdentity(id); err != nil {
		return err
	}
	verification := strings.TrimSpace(id.VerificationStatus)
	if verification == "" {
		verification = capregistry.SourceIdentityUnverified
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO source_identity_registry (`+sourceIdentityColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_type, source_key) DO UPDATE SET
			content_sha256      = excluded.content_sha256,
			source_version      = excluded.source_version,
			last_seen_at        = excluded.last_seen_at,
			verification_status = excluded.verification_status`,
		id.SourceType, id.SourceKey, id.ContentSHA256, strings.TrimSpace(id.SourceVersion),
		id.DiscoveredAt, id.LastSeenAt, verification,
	)
	if err != nil {
		return fmt.Errorf("source identity sqlite adapter: record (%s, %s): %w", id.SourceType, id.SourceKey, err)
	}
	return nil
}

// Count returns the total number of recorded source identities.
func (s *SourceIdentityStore) Count(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrSourceIdentityNotWired
	}
	var n int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM source_identity_registry`).Scan(&n); err != nil {
		return 0, fmt.Errorf("source identity sqlite adapter: count: %w", err)
	}
	return n, nil
}

func validateSourceIdentity(id capregistry.SourceIdentity) error {
	if strings.TrimSpace(id.SourceType) == "" {
		return fmt.Errorf("%w: empty source_type", capregistry.ErrSourceIdentityInvalid)
	}
	if strings.TrimSpace(id.SourceKey) == "" {
		return fmt.Errorf("%w: empty source_key", capregistry.ErrSourceIdentityInvalid)
	}
	if strings.TrimSpace(id.ContentSHA256) == "" {
		return fmt.Errorf("%w: empty content_sha256", capregistry.ErrSourceIdentityInvalid)
	}
	if strings.TrimSpace(id.DiscoveredAt) == "" {
		return fmt.Errorf("%w: empty discovered_at", capregistry.ErrSourceIdentityInvalid)
	}
	if strings.TrimSpace(id.LastSeenAt) == "" {
		return fmt.Errorf("%w: empty last_seen_at", capregistry.ErrSourceIdentityInvalid)
	}
	return nil
}
