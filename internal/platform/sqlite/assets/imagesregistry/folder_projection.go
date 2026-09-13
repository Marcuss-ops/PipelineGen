// Package imagesregistry — PostgreSQL clip_folders projection port.
//
// MEDIA-SSOT / POSTGRES-MEDIA-CUTOVER (September 2026): the catalog-sync
// folder reconciliation (ListFolders + DeleteFolder) moves onto the
// PostgreSQL media SSOT so the folder list and the media existence check read
// one engine. During the transition the operational SQLite clip_folders table
// remains the write-through owner: every write here is mirrored into the
// PostgreSQL projection through this port, and a boot-time backfill replays
// pre-cutover rows.
//
// The port is deliberately expressed with the domain type only
// (*detail.ClipFolder) so the infrastructure projection does not force
// platform/postgres to import this package.
package imagesregistry

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// FolderProjection mirrors clip_folders mutations into the PostgreSQL
// projection. Implemented by internal/platform/postgres/media.FolderRepository.
//
// UpsertFolder is the read-modify-write shape used by clip folder recording
// (ON CONFLICT DO UPDATE); InsertFolderIfAbsent preserves the legacy
// INSERT OR IGNORE semantics of the Drive folder resolver so re-resolving a
// known folder never clobbers aggregate counters.
type FolderProjection interface {
	UpsertFolder(ctx context.Context, folder *detail.ClipFolder) error
	InsertFolderIfAbsent(ctx context.Context, folder *detail.ClipFolder) error
	DeleteFolder(ctx context.Context, id string) error
}

// SetFolderProjection attaches the PostgreSQL clip_folders projection. Nil is
// accepted: the SQLite-only compositions (tests, degraded mode) keep the
// legacy single-store behaviour.
func (s *AssetStoreSQLite) SetFolderProjection(projection FolderProjection) {
	if s == nil {
		return
	}
	s.folderProjection = projection
}

// FolderProjectionWired reports whether the PostgreSQL folder projection is
// attached. Catalog sync uses this to decide whether the folder list can be
// read from PostgreSQL or must stay on the operational SQLite table.
func (s *AssetStoreSQLite) FolderProjectionWired() bool {
	return s != nil && s.folderProjection != nil
}

// mirrorFolderUpsert mirrors an UpsertFolder write into PostgreSQL.
func (s *AssetStoreSQLite) mirrorFolderUpsert(ctx context.Context, folder *detail.ClipFolder) error {
	if s == nil || s.folderProjection == nil || folder == nil {
		return nil
	}
	return s.folderProjection.UpsertFolder(ctx, folder)
}

// mirrorFolderInsertIfAbsent mirrors an INSERT OR IGNORE write into PostgreSQL.
func (s *AssetStoreSQLite) mirrorFolderInsertIfAbsent(ctx context.Context, folder *detail.ClipFolder) error {
	if s == nil || s.folderProjection == nil || folder == nil {
		return nil
	}
	return s.folderProjection.InsertFolderIfAbsent(ctx, folder)
}

// mirrorFolderDelete mirrors a DeleteFolder write into PostgreSQL.
func (s *AssetStoreSQLite) mirrorFolderDelete(ctx context.Context, id string) error {
	if s == nil || s.folderProjection == nil {
		return nil
	}
	return s.folderProjection.DeleteFolder(ctx, id)
}

// BackfillFolderProjection replays every operational clip_folders row into the
// PostgreSQL projection with ignore-on-conflict semantics. It is the BACKFILL
// phase of the folder-projection cutover (EXPAND → BACKFILL → CUTOVER): rows
// written before the projection existed are adopted without clobbering a
// projection row that is already newer. Idempotent — safe to run at every
// boot, and a no-op when no projection is wired.
func (s *AssetStoreSQLite) BackfillFolderProjection(ctx context.Context) (int, error) {
	if s == nil || s.folderProjection == nil {
		return 0, nil
	}
	folders, err := s.ListFolders(ctx, "")
	if err != nil {
		return 0, err
	}
	for _, folder := range folders {
		if folder == nil {
			continue
		}
		if err := s.folderProjection.InsertFolderIfAbsent(ctx, folder); err != nil {
			return 0, err
		}
	}
	return len(folders), nil
}
