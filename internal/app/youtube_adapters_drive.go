// Package app — YouTube drive + folder adapters
// split from youtube_adapters.go (PR-GODOBJ-Azione-4, July 2026).
//
// 3 adapters: driveFolderMgrAdapter, folderMemoryAdapter, sourcingDriveAdapter.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
)

// ── driveFolderMgrAdapter ─────────────────────────────────────────────

type driveFolderMgrAdapter struct {
	drive driveutil.Admin
	log   *zap.Logger
}

func newDriveFolderMgrAdapter(admin driveutil.Admin, log *zap.Logger) youtubeports.DriveFolderManagerPort {
	if admin == nil {
		return nil
	}
	return &driveFolderMgrAdapter{drive: admin, log: log}
}

func (a *driveFolderMgrAdapter) GetOrCreateFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if a.drive == nil {
		return parentFolderID, fmt.Errorf("driveFolderMgr: drive not wired")
	}
	return a.drive.GetOrCreateFolder(ctx, channelName, parentFolderID)
}

func (a *driveFolderMgrAdapter) UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*youtubeports.UploadResultDTO, bool, error) {
	if a.drive == nil {
		return nil, false, fmt.Errorf("driveFolderMgr: drive not wired")
	}
	res, skipped, err := a.drive.UploadFileIfChanged(ctx, localPath, folderID, filename)
	if err != nil {
		return nil, skipped, err
	}
	if res == nil {
		return nil, skipped, nil
	}
	return &youtubeports.UploadResultDTO{FileID: res.FileID, WebViewLink: res.WebViewLink}, skipped, nil
}

// ── folderMemoryAdapter ───────────────────────────────────────────────

type folderMemoryAdapter struct {
	inner *foldermemory.Service
}

func newFolderMemoryAdapter(svc *foldermemory.Service) youtubeports.FolderMemoryPort {
	if svc == nil {
		return nil
	}
	return &folderMemoryAdapter{inner: svc}
}

func (a *folderMemoryAdapter) LoadManifest(manifestPath string) (*asset.ClipManifest, error) {
	return a.inner.LoadManifest(manifestPath)
}
func (a *folderMemoryAdapter) SaveManifest(manifestPath string, manifest *asset.ClipManifest) error {
	return a.inner.SaveManifest(manifestPath, manifest)
}
func (a *folderMemoryAdapter) UpdateManifestTXT(folder *asset.ClipFolder, manifest *asset.ClipManifest) error {
	return a.inner.UpdateManifestTXT(folder, manifest)
}
func (a *folderMemoryAdapter) ComputeManifestStats(manifest *asset.ClipManifest) asset.ClipFolderStats {
	return a.inner.ComputeManifestStats(manifest)
}

// ── sourcingDriveAdapter ──────────────────────────────────────────────
// Merged from youtube_drive_legacy_adapter.go (PR-GODOBJ-Azione-4, July 2026).

type sourcingDriveAdapter struct {
	drive driveutil.Admin
}

// Compile-time pin: sourcingDriveAdapter satisfies sourcing.DrivePort.
var _ sourcing.DrivePort = (*sourcingDriveAdapter)(nil)

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
