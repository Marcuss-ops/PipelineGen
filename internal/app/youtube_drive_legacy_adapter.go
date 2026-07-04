// Package app — sourcing drive + clip-store adapters extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
//
// sourcingDriveAdapter is the legacy drive upload seam (DRIVE-008,
// dies post-cutover per the god-object decomposition plan).
package app

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	assetsrepo "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// ── sourcingDriveAdapter (legacy — DRIVE-008, dies post-cutover) ──────

type sourcingDriveAdapter struct {
	drive driveutil.Admin
}

// Compile-time pin: sourcingDriveAdapter satisfies sourcing.DrivePort.
// P2.2 (DRIVE-008, July 2026): mirrors the `var _ clips.ClipDriveUploaderPort =
// (*clipsDriveAdapter)(nil)` pin in clips_adapters_drive.go. Interface
// drift on sourcing.DrivePort surfaces at compile time rather than at
// first runtime call. After DRIVE-008 CUTOVER, the UploadFileWithDescription
// method body returns the fail-closed sentinel; the interface compliance
// is preserved because the method signature is unchanged.
var _ sourcing.DrivePort = (*sourcingDriveAdapter)(nil)

// UploadFileWithDescription removed in DRIVE-008 CUTOVER (July 2026).
// The legacy upload seam is retired; the canonical path is sourcing.PublisherPort.Publish.
// The sourcing.DrivePort interface no longer carries this method.

func (a *sourcingDriveAdapter) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if a.drive == nil {
		return parentID, fmt.Errorf("drive not configured")
	}
	return a.drive.GetOrCreateFolder(ctx, name, parentID)
}

func (a *sourcingDriveAdapter) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if a.drive == nil {
		return "", fmt.Errorf("drive not configured")
	}
	return a.drive.GetFolderName(ctx, folderID)
}

// ── sourcingClipStoreAdapter ──────────────────────────────────────────

// QDRANT-asset-mutation isolation (June 2026): UpsertClip was
// REMOVED from sourcing.ClipStorePort. The adapter above no longer
// exposes UpsertClip because there is no legitimate production caller;
// sourcing callers MUST go through IndexDispatcherPort. The
// non-dispatcher fallback in sourcing.Service.RegisterFromYouTube was
// also removed and replaced with a typed error so a missing
// dispatcher is loud at runtime, not silent.

type sourcingClipStoreAdapter struct {
	repo *assetsrepo.ClipsRepository
}

func (a *sourcingClipStoreAdapter) FindByName(ctx context.Context, name string) (string, error) {
	if a.repo == nil {
		return "", nil
	}
	return a.repo.FindByName(ctx, name)
}

func (a *sourcingClipStoreAdapter) FindExisting(ctx context.Context, videoID, url string, startSec, endSec float64) (string, error) {
	if a.repo == nil {
		return "", nil
	}
	hasSegment := endSec > startSec
	if videoID != "" {
		if id, err := a.repo.FindByYouTubeVideoID(ctx, videoID, hasSegment, startSec, endSec); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	if url != "" && !hasSegment {
		if id, err := a.repo.FindBySourceURL(ctx, url); err == nil && id != "" {
			return id, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", nil
}

func (a *sourcingClipStoreAdapter) GetClip(ctx context.Context, id string) (*sourcing.ExistingClip, error) {
	if a.repo == nil {
		return nil, nil
	}
	clip, err := a.repo.GetClip(ctx, id)
	if err != nil || clip == nil {
		return nil, err
	}
	return toExistingClip(clip), nil
}
