// Package mediaregistry — content_objects.go: SQLite adapter for the CAS
// content registry. Implements capregistry.ContentObjectStore against the
// content_objects table created by migrations/sqlite/194_content_objects.sql.
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure path returns a typed error
// or the nil-return convention documented on the port. A nil database
// surfaces ErrContentObjectsNotWired instead of a silent success.
package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// ErrContentObjectsNotWired is returned when the store is constructed with a
// nil *sql.DB (composition error, not a runtime availability event).
var ErrContentObjectsNotWired = errors.New("content objects sqlite adapter: not wired")

// ContentObjectStore is the SQLite implementation of the CAS content
// registry. It stores registry rows only; the bytes live in the
// content-addressed blob store.
type ContentObjectStore struct {
	db *sql.DB
}

// NewContentObjectStore constructs the adapter. Fail-fast: a nil database is
// a programmer error, not a silent no-op (godlike/07).
func NewContentObjectStore(db *sql.DB) (*ContentObjectStore, error) {
	if db == nil {
		return nil, errors.New("content objects sqlite adapter: nil database")
	}
	return &ContentObjectStore{db: db}, nil
}

// Compile-time pin: *ContentObjectStore satisfies the port.
var _ capregistry.ContentObjectStore = (*ContentObjectStore)(nil)

const contentObjectColumns = `sha256, size_bytes, mime_type, storage_uri, created_at, verified_at, integrity_status`

// Put upserts a content object row keyed by SHA-256. Idempotent on the
// digest; re-putting the same sha256 merges the row.
func (s *ContentObjectStore) Put(ctx context.Context, obj capregistry.ContentObject) error {
	if s == nil || s.db == nil {
		return ErrContentObjectsNotWired
	}
	if err := validateContentObject(obj); err != nil {
		return err
	}
	integrity := strings.TrimSpace(obj.IntegrityStatus)
	if integrity == "" {
		integrity = capregistry.IntegrityUnverified
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO content_objects (`+contentObjectColumns+`)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?)
		ON CONFLICT(sha256) DO UPDATE SET
			size_bytes       = excluded.size_bytes,
			mime_type        = excluded.mime_type,
			storage_uri      = excluded.storage_uri,
			verified_at      = excluded.verified_at,
			integrity_status = excluded.integrity_status`,
		obj.SHA256, obj.SizeBytes, strings.TrimSpace(obj.MimeType),
		obj.StorageURI, obj.CreatedAt, obj.VerifiedAt, integrity,
	)
	if err != nil {
		return fmt.Errorf("content objects sqlite adapter: put %q: %w", obj.SHA256, err)
	}
	return nil
}

// Get returns the content object for sha256, or (nil, nil) when absent.
func (s *ContentObjectStore) Get(ctx context.Context, sha256 string) (*capregistry.ContentObject, error) {
	if s == nil || s.db == nil {
		return nil, ErrContentObjectsNotWired
	}
	if strings.TrimSpace(sha256) == "" {
		return nil, fmt.Errorf("%w: empty sha256", capregistry.ErrContentObjectInvalid)
	}
	var (
		obj           capregistry.ContentObject
		verifiedAtRaw sql.NullString
		mimeRaw       sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT `+contentObjectColumns+`
		FROM content_objects
		WHERE sha256 = ?`, sha256).Scan(
		&obj.SHA256, &obj.SizeBytes, &mimeRaw, &obj.StorageURI,
		&obj.CreatedAt, &verifiedAtRaw, &obj.IntegrityStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("content objects sqlite adapter: get %q: %w", sha256, err)
	}
	if mimeRaw.Valid {
		obj.MimeType = mimeRaw.String
	}
	if verifiedAtRaw.Valid {
		obj.VerifiedAt = verifiedAtRaw.String
	}
	return &obj, nil
}

// Delete removes the registry row for sha256. Idempotent: deleting a missing
// object is a no-op success (physical blob cleanup belongs to the scanner).
func (s *ContentObjectStore) Delete(ctx context.Context, sha256 string) error {
	if s == nil || s.db == nil {
		return ErrContentObjectsNotWired
	}
	if strings.TrimSpace(sha256) == "" {
		return fmt.Errorf("%w: empty sha256", capregistry.ErrContentObjectInvalid)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM content_objects WHERE sha256 = ?`, sha256); err != nil {
		return fmt.Errorf("content objects sqlite adapter: delete %q: %w", sha256, err)
	}
	return nil
}

// Verify marks the object as IntegrityVerified at verifiedAt.
func (s *ContentObjectStore) Verify(ctx context.Context, sha256 string, verifiedAt string) error {
	if s == nil || s.db == nil {
		return ErrContentObjectsNotWired
	}
	if strings.TrimSpace(sha256) == "" {
		return fmt.Errorf("%w: empty sha256", capregistry.ErrContentObjectInvalid)
	}
	if strings.TrimSpace(verifiedAt) == "" {
		return fmt.Errorf("%w: empty verified_at", capregistry.ErrContentObjectInvalid)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE content_objects
		SET verified_at = ?, integrity_status = ?
		WHERE sha256 = ?`, verifiedAt, capregistry.IntegrityVerified, sha256)
	if err != nil {
		return fmt.Errorf("content objects sqlite adapter: verify %q: %w", sha256, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: %q", capregistry.ErrContentObjectNotFound, sha256)
	}
	return nil
}

// Count returns the total number of registered content objects.
func (s *ContentObjectStore) Count(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, ErrContentObjectsNotWired
	}
	var n int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM content_objects`).Scan(&n); err != nil {
		return 0, fmt.Errorf("content objects sqlite adapter: count: %w", err)
	}
	return n, nil
}

func validateContentObject(obj capregistry.ContentObject) error {
	if strings.TrimSpace(obj.SHA256) == "" {
		return fmt.Errorf("%w: empty sha256", capregistry.ErrContentObjectInvalid)
	}
	if strings.TrimSpace(obj.StorageURI) == "" {
		return fmt.Errorf("%w: empty storage_uri", capregistry.ErrContentObjectInvalid)
	}
	if strings.TrimSpace(obj.CreatedAt) == "" {
		return fmt.Errorf("%w: empty created_at", capregistry.ErrContentObjectInvalid)
	}
	if obj.SizeBytes < 0 {
		return fmt.Errorf("%w: negative size_bytes %d", capregistry.ErrContentObjectInvalid, obj.SizeBytes)
	}
	return nil
}
