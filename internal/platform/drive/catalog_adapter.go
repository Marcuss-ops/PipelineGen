// Package drive — catalog_adapter.go (DoD item 6, July 2026)
//
// Bridges the drive_folder_catalog SQLite repository to the
// drive.CatalogFolderLookup port so the Publisher can consult
// the local catalog before making Drive API calls.
//
// godlike/06 SSOT: this adapter is the canonical SOLE owner of the
// repository→port translation. The CatalogFolderLookup port lives in
// publisher.go; this concrete adapter lives here.
package drive

import (
	"context"
	"errors"

	sqlitedelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/delivery"
)

// NewCatalogFolderLookup creates a CatalogFolderLookup backed by the
// given catalog repository. Returns nil when repo is nil (nil-tolerant
// — the Publisher handles a nil CatalogFolderLookup gracefully by
// falling back to the Drive EnsureFolder API path).
func NewCatalogFolderLookup(repo *sqlitedelivery.Repository) CatalogFolderLookup {
	if repo == nil {
		return nil
	}
	return &catalogFolderLookupAdapter{repo: repo}
}

// catalogFolderLookupAdapter wraps a *sqlitedelivery.Repository to
// implement CatalogFolderLookup. Translates the repository's
// ErrNotFound into an empty return value so the Publisher
// treats "not in catalog" the same as "no catalog wired".
type catalogFolderLookupAdapter struct {
	repo *sqlitedelivery.Repository
}

func (a *catalogFolderLookupAdapter) RecordFolder(ctx context.Context, destination, path, folderID, parentFolderID string) error {
	if a == nil || a.repo == nil {
		return nil
	}
	_, err := a.repo.Upsert(ctx, nil, &sqlitedelivery.CatalogEntry{
		Destination:    destination,
		Namespace:      "voiceovers",
		Path:           path,
		FolderID:       folderID,
		ParentFolderID: parentFolderID,
		Source:         sqlitedelivery.SourceCreated,
		Status:         sqlitedelivery.StatusActive,
	})
	return err
}

var _ CatalogFolderWriter = (*catalogFolderLookupAdapter)(nil)

func (a *catalogFolderLookupAdapter) LookupFolder(ctx context.Context, destination, path string) (string, error) {
	entry, err := a.repo.FindByDestinationAndPath(ctx, destination, path)
	if err != nil {
		// Not-found is not an error at this level — the Publisher
		// treats an empty return as "no cached entry, fall back to Drive".
		if errors.Is(err, sqlitedelivery.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	// Only active entries with a non-empty folder_id are usable.
	if entry.Status != sqlitedelivery.StatusActive || entry.FolderID == "" {
		return "", nil
	}
	return entry.FolderID, nil
}
