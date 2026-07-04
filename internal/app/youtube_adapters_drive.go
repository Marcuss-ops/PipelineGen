// Package app — YouTube drive + folder adapters
// split from youtube_adapters.go (PR-GODOBJ-Azione-4, July 2026).
//
// 3 adapters: driveFolderMgrAdapter (legacy, wraps drive.Admin),
// YouTubePublisherDriveAdapter (canonical, wraps delivery.Publisher),
// folderMemoryAdapter.
// FASE 0.3 (July 2026): sourcingDriveAdapter retired via
// PR-YT-DRIVE-LEGACY-RETIRE; the canonical Publisher-port path
// (delivery.Publisher.Publish, FASE 5 since June 2026) is the sole
// Drive upload canal for the YouTube registrar.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
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

// ── YouTubePublisherDriveAdapter (canonical, wraps delivery.Publisher) ─

// YouTubePublisherDriveAdapter bridges the legacy
// youtubeports.DriveFolderManagerPort interface to the canonical
// delivery.Publisher.Publish surface. Phase 1d (July 2026): the
// adapter is wired in build_bundles_domain.go alongside the legacy
// driveFolderMgrAdapter; a future CUTOVER wave will retire the legacy
// adapter entirely so all YouTube→Drive traffic routes through the
// canonical Publisher.
//
// GetOrCreateFolder delegates folder resolution to
// Publisher.ResolveFolder, passing the channel name as the Group
// metadata for path-building.
//
// UploadFileIfChanged delegates to Publisher.Publish with
// ConflictSkipByHash, so the Publisher's content-dedupe logic
// (hash comparison) replaces the legacy Uploader.UploadFileIfChanged
// (filename-based lookup + MD5 comparison). The skipped bool and
// UploadResultDTO fields are derived from PublishResult.Action +
// PublishResult.FileID/PublishResult.WebViewLink.
type YouTubePublisherDriveAdapter struct {
	publisher delivery.Publisher
	log       *zap.Logger
}

// NewYouTubePublisherDriveAdapter returns the canonical adapter.
// pub must be non-nil (the caller — build_bundles_domain.go —
// asserts this).
func NewYouTubePublisherDriveAdapter(pub delivery.Publisher, log *zap.Logger) *YouTubePublisherDriveAdapter {
	return &YouTubePublisherDriveAdapter{publisher: pub, log: log}
}

// Compile-time assertion: adapter satisfies DriveFolderManagerPort.
var _ youtubeports.DriveFolderManagerPort = (*YouTubePublisherDriveAdapter)(nil)

func (a *YouTubePublisherDriveAdapter) GetOrCreateFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	// godlike/07 honest-limitation (Phase 1d, July 2026): the old
	// driveFolderMgrAdapter called drive.Admin.GetOrCreateFolder(ctx,
	// channelName, parentFolderID) — channelName was the literal Drive
	// folder name created under parentFolderID. This adapter passes
	// channelName as Group metadata + parentFolderID as
	// RootFolderOverride to Publisher.ResolveFolder, which resolves the
	// full path via the DestinationRegistry (clips/{channelName} instead
	// of {parentFolderID}/{channelName}). The folder hierarchy MAY differ
	// from the legacy path; callers that rely on exact folder paths
	// (ensureChannelFolder → subsequent uploads) should verify the
	// Publisher's registry configuration produces the expected hierarchy.
	if a.publisher == nil {
		return parentFolderID, fmt.Errorf("YouTubePublisherDriveAdapter.GetOrCreateFolder: publisher not wired")
	}
	folderID, err := a.publisher.ResolveFolder(ctx, delivery.PublishRequest{
		Destination:       delivery.DestinationYouTubeClip,
		Group:             channelName,
		RootFolderOverride: parentFolderID,
	})
	if err != nil {
		return parentFolderID, fmt.Errorf("YouTubePublisherDriveAdapter.GetOrCreateFolder: %w", err)
	}
	return folderID, nil
}

func (a *YouTubePublisherDriveAdapter) UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*youtubeports.UploadResultDTO, bool, error) {
	if a.publisher == nil {
		return nil, false, fmt.Errorf("YouTubePublisherDriveAdapter.UploadFileIfChanged: publisher not wired")
	}
	result, err := a.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:       delivery.DestinationYouTubeClip,
		LocalPath:         localPath,
		Filename:          filename,
		RootFolderOverride: folderID,
		ConflictPolicy:    delivery.ConflictSkipByHash,
	})
	if err != nil {
		return nil, false, fmt.Errorf("YouTubePublisherDriveAdapter.UploadFileIfChanged: %w", err)
	}
	// UploadOutcomeSkipped is the canonical sentinel (PublishActionSkipped
	// is the same value via type alias; only one check needed).
	skipped := result.Action == delivery.UploadOutcomeSkipped
	return &youtubeports.UploadResultDTO{
		FileID:      result.FileID,
		WebViewLink: result.WebViewLink,
	}, skipped, nil
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
// FASE 0.3 (July 2026): sourcingDriveAdapter + sourcing.DrivePort
// retired per PR-YT-DRIVE-LEGACY-RETIRE (godlike/07 no-fake-availability
// — zero live concrete remained). The 2 surviving methods (GetOrCreateFolder
// + GetFolderName) had no production caller; YouTube drive uploads
// route through delivery.Publisher.Publish (canonical, FASE 5).
// See architecture/deprecations.yaml#PR-YT-DRIVE-LEGACY-RETIRE.
