// Package adminconsole provides SQLite-backed implementations of the
// admin console ports defined in internal/capabilities/adminconsole
package adminconsole

import (
	"context"
	"database/sql"

	appadminconsole "github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminconsole"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
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
	return imagesregistry.CheckAndIncrementMediaAssetVersion(ctx, s.db, assetID, expectedVersion)
}

// Compile-time check.
var _ appadminconsole.EntityVersionStore = (*VersionStore)(nil)
