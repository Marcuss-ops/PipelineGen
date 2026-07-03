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

// UploadFileWithDescription is the legacy drive upload seam for
// the sourcing layer. DRIVE-008 (July 2026): retired to fail-closed
// per godlike/07 §"No fake availability" — invoked callers receive
// drive.ErrLegacySurfaceRetired immediately at runtime, no silent
// fallback. Canonical upload path is delivery.Publisher.Publish
// (sourcingDriven layer migrates to sourcing.PublisherPort which
// wraps delivery.Publisher, see internal/application/assets/
// sourcing/ports.go::PublisherPort).
//
// Compile-time assembly: sourcingDriveAdapter satisfies
// sourcing.DrivePort (and indirectly the CompositionRoot's typed
// wiring). The interface signature still matches the legacy shape
// even though the body is now a fail-closed shim — callers detect
// the failure via errors.Is(err, drive.ErrLegacySurfaceRetired).
// P2.6 (DRIVE-CUTOVER-P0-1): sourcing-side multi-wrap mirrors the
// clips adapter; the underlying driveutil.ErrLegacySurfaceRetired
// is preserved alongside the facade surface for any future
// forward-port. SourcingPkg doesn't currently have its own
// application-layer sentinel (the sourcing side is mid-renaming
// per the FASE 5 fallback retirement); the drive-package sentinel
// is the only probe the sourcing consumer uses. Errors.Is at the
// sourcing consumption layer will continue to resolve cleanly.
func (a *sourcingDriveAdapter) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*sourcing.DriveUploadResult, error) {
	return nil, fmt.Errorf("sourcingDriveAdapter.UploadFileWithDescription(localPath=%q folderID=%q filename=%q) retired by DRIVE-008: %w", localPath, folderID, filename, driveutil.ErrLegacySurfaceRetired)
}

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
