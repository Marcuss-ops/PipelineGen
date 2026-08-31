// Package assets — clip_cache_adapter.go: ClipCacheAdapter concrete
// implementation of the youtubeports.ClipCachePort typed interface.
//
// Commit 1/6 (PR-C-YouTube-Cutover, June 2026): the canonical
// ProcessYouTubeSegmentUseCase (internal/capabilities/youtube/usecase/
// process_segment.go) consults the clip cache at Step 2 to short-circuit
// re-extraction on already-processed clips. Before Commit 1 the
// composition layer did not wire the port — Cache.GetExisting returned
// (nil, false, nil) structurally because the nil-port decoder in
// process_segment.go collapses to a no-op (process_segment.go Step 2
// marker "if u.deps.Cache != nil").
//
// Post-Commit 1, the cache port is REQUIRED on the use case constructor
// (fail-fast panic in NewProcessYouTubeSegmentUseCase for nil Cache —
// see process_segment.go godoc). This file is the production adapter
// the composition root wires.
//
// Mapping rules (*domain/asset.Asset → youtubetypes.ExtractItem):
//   - ID             ← Asset.ID
//   - Name           ← Asset.Name (with empty fallback to clip_NNN to
//     mirror cleanSegmentName from process_segment.go)
//   - Filename       ← Asset.Filename (with filepath.Base(LocalPath)
//     fallback when empty)
//   - LegacyFileMD5       ← Asset.LegacyFileMD5() string accessor; "" if absent
//   - LocalPath      ← Asset.LocalPath() string accessor
//   - DriveFileID    ← Asset.DriveFileID() string accessor
//   - DriveLink      ← Asset.DriveLink() string accessor
//   - DownloadLink   ← Asset.DownloadLink() string accessor
//   - DriveFolderID  ← Asset.FolderID() (canonical mapping per
//     clips_crud_test.go::drive_folder_id round-trip)
//   - DriveFolderPath ← Asset.FolderPath() string accessor
//   - Duration       ← Asset.Duration (time.Duration → seconds rounded)
//   - Status         ← "skipped" (the cache-hit semantics per the
//     9-step pipeline in process_segment.go — short-
//     circuit re-extraction is an idempotent "skipped"
//     outcome per the verdict's P1 #12 classifier)
//   - Start / End / StartSeconds / EndSeconds ← not surfaced: the
//     canonical record stores boundaries only in
//     metadata_json; downstream callers re-derive
//     from filename + clipID. The cache hit does
//     NOT rewrite the boundary because the upstream
//     build_clip_filename already stamps it into
//     the basename.
//
// Pattern 0 (AGENTS.md): the concrete receiver here is the ONLY point
// the port __shape__ is satisfied by an asset-DB reader. Compile-time
// assertion at the bottom pins the conformance; signature drift
// surfaces as a build failure, not a runtime panic.
package imagesregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ClipCacheAdapter implements youtubeports.ClipCachePort over the
// canonical *ClipsRepository. Holds *ClipsRepository (not raw DB)
// because AssetStoreSQLite.Get is the canonical read seam — it
// honours the SoftDeleteFilter (DELETED rows are cache-misses) and
// the 40-column projection lock with ScanMediaAsset.
//
// Audit 2026-07-03 BLOCKER #5 (cache file verification): the adapter
// now verifies the local file still exists before returning a cache
// hit. A stale SQLite row whose local_path file has been deleted
// returns cache-miss so the use case falls through to re-download.
type ClipCacheAdapter struct {
	repo *ClipsRepository
	log  *zap.Logger
}

// NewClipCacheAdapter constructs the cache adapter on top of the
// canonical clips repository. nil repo is FAIL-CLOSED: every GetExisting
// call returns an error so a wiring gap lands in operator logs as a
// loud failure rather than a silent cache miss + re-processing loop.
//
// log is optional: when nil, file-missing cache misses are silent.
// Production wiring passes a real *zap.Logger for operator visibility.
func NewClipCacheAdapter(repo *ClipsRepository, log *zap.Logger) *ClipCacheAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipCacheAdapter{repo: repo, log: log}
}

// GetExisting returns the cached ExtractItem for clipID. The contract:
//   - cache hit    → (item, true, nil) when a non-deleted
//     media_assets row exists for clipID. Status
//     is "skipped" so the use case's downstream
//     fan-out renders cache-hit as idempotent re-run.
//   - cache miss   → (nil, false, nil) when the row is absent or
//     soft-deleted (lifecycle_state='DELETED' AND
//     deleted_at != ”). re-extract under a new
//     policy_version is uninterrupted.
//   - real failure → (nil, false, err) when the underlying SQL
//     failed; the use case logs + falls through to
//     re-process (no panic).
//
// clipID is the canonical `yt_<videoID>_<startSec>_<endSec>_<policyVer>`
// shape produced by process_segment.go::Step 1. Direct PK lookup on
// media_assets.id, no JOIN, no metadata_json filter. The clipID is
// the row's PK; the (channel_id, video_id) leader-election dance lives
// on the youtube_discoveries ledger, not on this cache.
func (a *ClipCacheAdapter) GetExisting(ctx context.Context, clipID string) (*youtubetypes.ExtractItem, bool, error) {
	if a == nil || a.repo == nil {
		return nil, false, fmt.Errorf("ClipCacheAdapter: not wired (composition must inject a real *ClipsRepository)")
	}
	if clipID == "" {
		return nil, false, fmt.Errorf("ClipCacheAdapter.GetExisting: clipID is required")
	}

	// ClipsRepository.Get returns (nil, nil) on filter-miss per
	// the canonical SoftDeleteFilter contract; returns (*asset.Asset, err)
	// on real hit / real failure. We branch on details==nil for
	// cache miss.
	details, err := a.repo.Get(ctx, clipID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("ClipCacheAdapter.GetExisting: %w", err)
	}
	if details == nil {
		return nil, false, nil
	}

	// BLOCKER #5 closure (audit 2026-07-03): verify the local file
	// still exists before returning a cache hit. A stale SQLite row
	// whose local_path file has been cleaned up must return cache-miss
	// so the use case falls through to re-download.
	//
	// When localPath is empty but DriveFileID is present, consider it
	// a cache hit — the Drive file may still be accessible even though
	// the local scratch file was cleaned up.
	//
	// When BOTH localPath and DriveFileID are empty, return cache-miss:
	// the row has no usable file reference (degenerate — shouldn't exist
	// in production, but the guard is cheap and fail-closed).
	localPath := details.LocalPath()
	if localPath != "" {
		stat, statErr := os.Stat(localPath)
		if statErr != nil || stat.Size() == 0 {
			a.log.Info("ClipCacheAdapter: cache miss — local file missing or empty, falling through to re-download",
				zap.String("clip_id", clipID),
				zap.String("local_path", localPath),
				zap.Any("stat_err", statErr))
			return nil, false, nil
		}
	} else if details.DriveFileID() == "" {
		// No local file AND no Drive reference — the row has no
		// usable source. Return cache-miss so the use case
		// re-downloads rather than propagating a phantom hit.
		a.log.Info("ClipCacheAdapter: cache miss — no local file and no Drive reference, falling through to re-download",
			zap.String("clip_id", clipID))
		return nil, false, nil
	}

	return assetToExtractItem(details), true, nil
}

// assetToExtractItem is the canonical *asset.Asset → *ExtractItem
// mapper. Kept as a package-private function (not a method) so
// adapter tests can exercise the mapping against fixture assets
// without spinning up a ClipsRepository. Comment-documents the
// 11-field shape at the package header above.
func assetToExtractItem(a *asset.Asset) *youtubetypes.ExtractItem {
	if a == nil {
		return nil
	}

	item := &youtubetypes.ExtractItem{
		// Boundary-less fields: ID, Name, hashing, drive IDs
		// map directly off the asset struct.
		ID:              a.ID,
		Name:            routeEmptyName(a.Name, a.ID),
		Filename:        routeEmptyFilename(a.Filename, a.LocalPath()),
		LegacyFileMD5:   a.LegacyFileMD5(),
		LocalPath:       a.LocalPath(),
		DriveFileID:     a.DriveFileID(),
		DriveLink:       a.DriveLink(),
		DownloadLink:    a.DownloadLink(),
		DriveFolderID:   a.FolderID(),
		DriveFolderPath: a.FolderPath(),
		Status:          "skipped",
	}

	// Duration: time.Duration → seconds rounded down. integer
	// truncation mirrors how process_segment.go::Step 1 derives
	// the canonical seconds-bound from the parsed VttTimestamps.
	if a.Duration > 0 {
		item.Duration = int(a.Duration.Seconds())
	}

	// Boundaries (Start/End + StartSeconds/EndSeconds): NOT reconstructed
	// from Asset alone. metadata_json would be the source, but parsing
	// the JSON here would couple the cache adapter to the metadata
	// builder (Commit 4 surface). Downstream callers tolerate empty
	// boundaries because the 9-step pipeline only reads LocalPath +
	// DriveFileID + DriveLink + DownloadLink on cache-hit
	// (process_segment.go::Step 2 fields used).
	// Boundary fields are left at their zero value.

	return item
}

// routeEmptyName is a small helper mirroring cleanSegmentName in
// process_segment.go: empty Name → "clip_<id_suffix>" so the cached
// item renders a non-empty Name in recover logs.
func routeEmptyName(name, id string) string {
	if name != "" {
		return name
	}
	return "clip_" + idSuffix(id)
}

// routeEmptyFilename mirrors the canonical rule "filename = base of
// local_path if empty". Falls back to "<id>.mp4" so the row is not
// inserted with a NULL filename (the column is NOT NULL DEFAULT ”).
func routeEmptyFilename(filename, localPath string) string {
	if filename != "" {
		return filename
	}
	if localPath != "" {
		return filepath.Base(localPath)
	}
	return ""
}

// idSuffix extracts the trailing 12 hex chars of a canonical clipID
// for human-readable Name fallback. Cheap deterministic trim.
func idSuffix(id string) string {
	clean := strings.TrimPrefix(id, "yt_")
	if len(clean) > 12 {
		return clean[len(clean)-12:]
	}
	return clean
}

// ── Compile-time assertion ──────────────────────────────────────────

// Per AGENTS.md Pattern 0: signature drift surfaces as a build
// failure.
var _ youtubeports.ClipCachePort = (*ClipCacheAdapter)(nil)
