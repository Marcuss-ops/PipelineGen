package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// PostgresEntityVersionStore is the PostgreSQL media-SSOT implementation of
// the admin console optimistic-concurrency port
// (`CheckAndIncrementAssetVersion`).
//
// It is the PostgreSQL counterpart of the SQLite
// sqlite/adminconsole.VersionStore: ONE version port, two engine
// implementations, selected by composition. The check-and-bump stays a single
// atomic statement so two concurrent PATCHes cannot observe the same expected
// version.
type PostgresEntityVersionStore struct {
	db *sql.DB
}

// NewPostgresEntityVersionStore wires the version store over the media SSOT
// handle. A nil handle returns nil (degrade signal).
func NewPostgresEntityVersionStore(db *sql.DB) *PostgresEntityVersionStore {
	if db == nil {
		return nil
	}
	return &PostgresEntityVersionStore{db: db}
}

// CheckAndIncrementAssetVersion atomically increments media_assets.admin_version
// only when it currently equals expectedVersion. It returns the observed
// version and ok=true when the increment succeeded; otherwise ok=false with the
// version currently stored (mirroring the SQLite contract exactly).
func (s *PostgresEntityVersionStore) CheckAndIncrementAssetVersion(ctx context.Context, assetID string, expectedVersion int) (int, bool, error) {
	if s == nil || s.db == nil {
		return 0, false, errors.New("postgres media: version store not wired")
	}
	if strings.TrimSpace(assetID) == "" {
		return 0, false, errors.New("asset committer: asset id is required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE media_assets
		SET admin_version = admin_version + 1
		WHERE id = $1 AND admin_version = $2`, assetID, expectedVersion)
	if err != nil {
		return 0, false, fmt.Errorf("asset committer: increment admin version: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("asset committer: admin version rows affected: %w", err)
	}

	var currentVersion int
	if err := s.db.QueryRowContext(ctx, `SELECT admin_version FROM media_assets WHERE id = $1`, assetID).Scan(&currentVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, errors.New("asset committer: asset not found")
		}
		return 0, false, fmt.Errorf("asset committer: read admin version: %w", err)
	}
	return currentVersion, affected == 1, nil
}

// AdminVersion reads the current optimistic-concurrency version for an asset
// from the media SSOT (0 for a missing row, matching the admin console read).
func (s *PostgresAssetStore) AdminVersion(ctx context.Context, assetID string) (int, error) {
	if s == nil || s.reader == nil || s.reader.db == nil {
		return 0, errors.New("postgres media: asset store not wired")
	}
	var version int
	err := s.reader.db.QueryRowContext(ctx, `SELECT COALESCE(admin_version, 0) FROM media_assets WHERE id = $1`, assetID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres media: read admin version: %w", err)
	}
	return version, nil
}
