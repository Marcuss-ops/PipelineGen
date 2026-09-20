// cmd/admin/internal/audit/broken_references_media.go — the PostgreSQL half of
// the broken-references audit.
//
// MEDIA-SSOT (2026-09-20): the drive-reference, local-path and Qdrant-point
// checks in the parent file read media_assets, and media_assets is
// PostgreSQL-owned. The operational SQLite handle holds no committed media rows,
// so sweeping it there reported a clean bill of health over an empty table. The
// media-owned columns are therefore answered from the media SSOT through the
// narrow port below, and the operational sweeps skip media_assets by name.
//
// The split is also a file-size boundary: the parent file holds the audit's
// orchestration and the operational sweeps, this file holds the media engine
// boundary.
package audit

import (
	"context"
	"fmt"
	"os"

	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// brokenRefMediaSource is the narrow media read the audit depends on.
//
// MEDIA-SSOT: production binds the PostgreSQL media SSOT reader
// (pgmedia.MediaReferenceAuditReader). The port is deliberately engine-specific
// rather than a generic *sql.DB: a handle-typed port is exactly how the media
// read would drift back onto the operational store.
type brokenRefMediaSource interface {
	ListDriveFileRefs(ctx context.Context) ([]pgmedia.MediaDriveRef, error)
	ListLocalPaths(ctx context.Context) ([]pgmedia.MediaLocalPathRef, error)
	ListSearchEligibleAssetIDs(ctx context.Context) ([]string, error)
}

// mediaOwnedAuditTables are the tables the operational SQLite sweep must NOT
// read media rows from. They are answered by the PostgreSQL media SSOT via
// brokenRefMediaSource instead.
var mediaOwnedAuditTables = map[string]bool{"media_assets": true}

// detectBrokenMediaDriveRefs answers the media half of the Drive cross-check
// from the PostgreSQL media SSOT. The asset_id comes from the SSOT row itself,
// so no enrichment lookup is needed.
func detectBrokenMediaDriveRefs(ctx context.Context, media brokenRefMediaSource, knownIDs map[string]bool) ([]brokenDriveRef, int, []string, error) {
	if media == nil {
		return nil, 0, nil, fmt.Errorf("media SSOT reader is not wired")
	}
	refs, err := media.ListDriveFileRefs(ctx)
	if err != nil {
		return nil, 0, nil, err
	}
	var broken []brokenDriveRef
	for _, ref := range refs {
		if !knownIDs[ref.DriveFileID] {
			broken = append(broken, brokenDriveRef{
				Table:       "media_assets",
				Column:      "drive_file_id",
				RefValue:    ref.DriveFileID,
				AssetID:     ref.AssetID,
				FailureKind: "drive_file_not_found",
			})
		}
	}
	return broken, len(refs), nil, nil
}

// detectBrokenMediaLocalPaths answers the media half of the local-path
// cross-check from the PostgreSQL media SSOT. Existence is checked on the local
// filesystem, exactly as for the operational tables: a path that does not
// resolve is reported, never assumed readable.
func detectBrokenMediaLocalPaths(ctx context.Context, media brokenRefMediaSource) ([]brokenLocalRef, int, error) {
	if media == nil {
		return nil, 0, fmt.Errorf("media SSOT reader is not wired")
	}
	refs, err := media.ListLocalPaths(ctx)
	if err != nil {
		return nil, 0, err
	}
	var broken []brokenLocalRef
	for _, ref := range refs {
		info, statErr := os.Stat(ref.LocalPath)
		if statErr != nil {
			kind := "stat_error"
			if os.IsNotExist(statErr) {
				kind = "file_not_found"
			}
			broken = append(broken, brokenLocalRef{
				Table:       "media_assets",
				Column:      "local_path",
				LocalPath:   ref.LocalPath,
				FailureKind: kind,
				Error:       statErr.Error(),
			})
			continue
		}
		if info.IsDir() {
			// A directory is suspicious but not necessarily broken: artifact
			// caches legitimately point at directories.
			continue
		}
	}
	return broken, len(refs), nil
}
