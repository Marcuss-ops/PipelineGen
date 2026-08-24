// Package adminconsole provides SQLite-backed implementations of the
// admin console ports defined in internal/application/adminconsole.
package adminconsole

import (
	"context"
	"database/sql"
	"fmt"

	appadminconsole "github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminconsole"
)

// VersionStore is the SQLite-backed adminconsole.EntityVersionStore.
type VersionStore struct {
	db *sql.DB
}

// NewVersionStore creates a new version store backed by the given
// SQLite writer connection.
func NewVersionStore(db *sql.DB) *VersionStore {
	if db == nil {
		panic("adminconsole.NewVersionStore: nil db")
	}
	return &VersionStore{db: db}
}

// CheckAndIncrementAssetVersion atomically increments the admin_version
// column on media_assets only if it currently equals expectedVersion.
//
// It returns the new version and ok=true when the update succeeded.
// If no row was updated because the current version differed, it returns
// ok=false and the current version observed in the row.
//
// This primitive keeps the optimistic concurrency check inside the same
// UPDATE statement, avoiding read-modify-write races.
func (s *VersionStore) CheckAndIncrementAssetVersion(ctx context.Context, assetID string, expectedVersion int) (currentVersion int, ok bool, err error) {
	if assetID == "" {
		return 0, false, fmt.Errorf("CheckAndIncrementAssetVersion: empty asset id")
	}

	// Single atomic statement: bump only when the version matches.
	res, err := s.db.ExecContext(ctx, `
		UPDATE media_assets
		SET admin_version = admin_version + 1
		WHERE id = ? AND admin_version = ?
	`, assetID, expectedVersion)
	if err != nil {
		return 0, false, fmt.Errorf("CheckAndIncrementAssetVersion: update: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("CheckAndIncrementAssetVersion: rows affected: %w", err)
	}

	// Read back the current (now incremented) version for the caller.
	row := s.db.QueryRowContext(ctx, `SELECT admin_version FROM media_assets WHERE id = ?`, assetID)
	if err := row.Scan(&currentVersion); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, fmt.Errorf("CheckAndIncrementAssetVersion: asset not found")
		}
		return 0, false, fmt.Errorf("CheckAndIncrementAssetVersion: scan version: %w", err)
	}

	if affected == 0 {
		return currentVersion, false, nil
	}
	return currentVersion, true, nil
}

// Compile-time check.
var _ appadminconsole.EntityVersionStore = (*VersionStore)(nil)
