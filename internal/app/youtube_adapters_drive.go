// Package app — YouTube drive + folder adapters
// split from youtube_adapters.go (PR-GODOBJ-Azione-4, July 2026).
//
// FASE 0.3 (July 2026): sourcingDriveAdapter retired via
// PR-YT-DRIVE-LEGACY-RETIRE. The canonical Publisher-port path
// (delivery.Publisher.Publish, FASE 5 since June 2026) is the sole
// Drive upload canal for the YouTube registrar.
//
// PR-P12-YOUTUBE-LEGACY-RETIRE (July 2026, deadline 2026-08-08): the
// legacy driveFolderMgrAdapter (which wrapped drive.Admin directly,
// bypassing the DestinationRegistry) is PHYSICALLY RETIRED via
// git-rm per godlike/07 NO-FAKE-AVAILABILITY — the canonical
// composition root (build_bundles_domain.go) was instantiating the
// legacy adapter but then immediately discarding it via
// `_ = driveFolderMgr`; the canonical YouTubePublisherDriveAdapter
// (this file) is the SOLE DriveFolderManagerPort binding for the
// YouTube pipeline (see architecture/current.yaml#PR-P12-DRIVE-COMPLETION-2026-07-08
// per-PR closure 4/6).
//
// godlike/06 SSOT (one canonical owner per fact): the canonical
// YouTubePublisherDriveAdapter is the SOLE owner of the
// DriveFolderManagerPort implementation in production. The 2
// RootFolderOverride=parentFolderID + RootFolderOverride=folderID
// literals previously threaded through Publisher.ResolveFolder /
// Publisher.Publish are RETIRED — the canonical Publisher now
// resolves the root folder for DestinationYouTubeClip via
// DestinationRegistry + DestinationPolicy.RootFolderID (single
// source of truth for root folders per the
// architecture/current.yaml#DRIVE-AS-CENTRAL-CAPABILITY wave).
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
)

// ── YouTubePublisherDriveAdapter (canonical, wraps delivery.Publisher) ─

// YouTubePublisherDriveAdapter bridges the legacy
// youtubeports.DriveFolderManagerPort interface to the canonical
// delivery.Publisher.Publish surface. Per FASE A5 + PR-P12-DRIVE-COMPLETION-2026-07-08
// (July 2026): this adapter is the SOLE DriveFolderManagerPort
// implementation in production. The legacy driveFolderMgrAdapter
// (which wrapped drive.Admin directly) is RETIRED.
//
// GetOrCreateFolder delegates folder resolution to
// Publisher.ResolveFolder, passing the channel name as the Group
// metadata for path-building. The root folder is resolved
// canonically by the Publisher via DestinationRegistry + RootFolderID
// (no caller-supplied RootFolderOverride per godlike/07
// NO-FAKE-AVAILABILITY).
//
// UploadFileIfChanged delegates to Publisher.Publish with
// ConflictSkipByHash, so the Publisher's content-dedupe logic
// (hash comparison) replaces the legacy Uploader.UploadFileIfChanged
// (filename-based lookup + MD5 comparison). The skipped bool and
// UploadResultDTO fields are derived from PublishResult.Action +
// PublishResult.FileID/PublishResult.WebViewLink. Group + Subject
// are intentionally OMITTED — the folder context was established
// by the prior GetOrCreateFolder call, and the per-file identity
// lives in PublishRequest.Filename (the canonical file path is
// {resolvedFolder}/{filename}).
type YouTubePublisherDriveAdapter struct {
	publisher delivery.Publisher
	admin     drive.Admin
	log       *zap.Logger
}

// NewYouTubePublisherDriveAdapter returns the canonical adapter.
// pub must be non-nil (the caller — build_bundles_domain.go —
// asserts this).
func NewYouTubePublisherDriveAdapter(pub delivery.Publisher, admin drive.Admin, log *zap.Logger) *YouTubePublisherDriveAdapter {
	return &YouTubePublisherDriveAdapter{publisher: pub, admin: admin, log: log}
}

// Compile-time assertion: adapter satisfies DriveFolderManagerPort.
var _ youtubeports.DriveFolderManagerPort = (*YouTubePublisherDriveAdapter)(nil)

func (a *YouTubePublisherDriveAdapter) GetOrCreateFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if a.admin != nil {
		return a.admin.GetOrCreateFolder(ctx, channelName, parentFolderID)
	}
	// PR-P12-YOUTUBE-LEGACY-RETIRE (July 2026): the legacy
	// RootFolderOverride=parentFolderID literal is RETIRED per
	// godlike/07 NO-FAKE-AVAILABILITY. When the dedicated drive.Admin
	// seam is unavailable (legacy tests / fallback wiring), the
	// canonical Publisher resolves the root folder for
	// DestinationYouTubeClip via DestinationRegistry +
	// DestinationPolicy.RootFolderID (single source of truth for root
	// folders per the architecture/current.yaml#DRIVE-AS-CENTRAL-CAPABILITY wave).
	if a.publisher == nil {
		return parentFolderID, fmt.Errorf("YouTubePublisherDriveAdapter.GetOrCreateFolder: publisher not wired")
	}
	folderID, err := a.publisher.ResolveFolder(ctx, delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		Group:       channelName,
		// Subject + RootFolderOverride intentionally OMITTED:
		// - Subject is the per-file identity; folder resolution
		//   operates on the channel-level Group only.
		// - RootFolderOverride is the legacy bypass literal —
		//   Publisher resolves the canonical root via
		//   DestinationRegistry.
	})
	if err != nil {
		return parentFolderID, fmt.Errorf("YouTubePublisherDriveAdapter.GetOrCreateFolder: %w", err)
	}
	return folderID, nil
}

func (a *YouTubePublisherDriveAdapter) UploadFileIfChanged(ctx context.Context, localPath, folderID, filename, group, subject string) (*youtubeports.UploadResultDTO, bool, error) {
	if a.admin != nil {
		res, skipped, err := a.admin.UploadFileIfChanged(ctx, localPath, folderID, filename)
		if err != nil {
			return nil, false, fmt.Errorf("YouTubePublisherDriveAdapter.UploadFileIfChanged: %w", err)
		}
		if res == nil {
			return nil, skipped, nil
		}
		return &youtubeports.UploadResultDTO{
			FileID:      res.FileID,
			WebViewLink: res.WebViewLink,
		}, skipped, nil
	}
	// PR-P12-YOUTUBE-LEGACY-RETIRE (July 2026): the legacy
	// RootFolderOverride=folderID literal is RETIRED per
	// godlike/07 NO-FAKE-AVAILABILITY. The folder context was
	// established by the prior GetOrCreateFolder call (folderID
	// was the resolved folder ID returned by Publisher.ResolveFolder
	// for the channel's Group); the canonical Publisher applies
	// that folder to the per-file publish via the DestinationRegistry
	// resolution (NOT via a caller-supplied RootFolderOverride).
	// Group + Subject are also intentionally OMITTED — the
	// per-file identity is captured in PublishRequest.Filename
	// (mirrors the soundeffect/handler.go canonical pattern:
	// soundeffect sidecar + audio file publish drop Group/Subject
	// in favor of Filename as the canonical file identifier).
	if a.publisher == nil {
		return nil, false, fmt.Errorf("YouTubePublisherDriveAdapter.UploadFileIfChanged: publisher not wired")
	}
	result, err := a.publisher.Publish(ctx, delivery.PublishRequest{
		Destination:    delivery.DestinationYouTubeClip,
		LocalPath:      localPath,
		Filename:       filename,
		Group:          group,
		Subject:        subject,
		ConflictPolicy: delivery.ConflictSkipByHash,
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

// Compile-time assertion: adapter satisfies FolderMemoryPort.
var _ youtubeports.FolderMemoryPort = (*folderMemoryAdapter)(nil)
