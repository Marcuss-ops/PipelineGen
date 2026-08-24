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
// DriveFolderManagerPort implementation in production. Folder writes
// are threaded as DestinationFolderID (canonical leaf semantics — the
// upload lands directly in the payload-selected Drive folder); the
// legacy ParentFolderID escape hatch is used only by
// GetOrCreateFolder to pin the channel folder under a caller-selected
// Drive root. The canonical Publisher resolves the root folder for
// DestinationYouTubeClip via DestinationRegistry +
// DestinationPolicy.RootFolderID (single source of truth for root
// folders per the architecture/current.yaml#DRIVE-AS-CENTRAL-CAPABILITY
// wave).
package app

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── YouTubePublisherDriveAdapter (canonical, wraps delivery.Publisher) ─

// YouTubePublisherDriveAdapter bridges the legacy
// youtubeports.DriveFolderManagerPort interface to the canonical
// delivery.Publisher.Publish surface. Per FASE A5 + PR-P12-DRIVE-COMPLETION-2026-07-08
// (July 2026): this adapter is the SOLE DriveFolderManagerPort
// implementation in production. The legacy driveFolderMgrAdapter
// (which wrapped drive.Admin directly) is RETIRED.
//
// godlike/06 SSOT (one canonical owner per fact): delivery.Publisher
// is the SOLE canonical write seam for YouTubeClipPath (Group +
// Subject + DestinationRegistry). drive.Admin is intentionally NOT
// exposed here — it carries NO delivery semantics, and re-introducing
// it would silently drop Group/Subject (code-reviewer M1+M2 of the
// prior `0a7f7995` commit). Per godlike/07 NO-FAKE-AVAILABILITY,
// there is exactly one Drive write path for the YouTube pipeline:
// publisher.Publish. A future migration that ever needs to also
// call drive.Admin (e.g. for a Drive-only housekeeping op) MUST
// route through the composition root, NOT re-add an admin seam here.
//
// GetOrCreateFolder delegates folder resolution to
// Publisher.ResolveFolder, passing the channel name as the Group
// metadata for path-building. The root folder is resolved
// canonically by the Publisher via DestinationRegistry + RootFolderID
// (no caller-supplied ParentFolderID per godlike/07
// NO-FAKE-AVAILABILITY).
//
// UploadFileIfChanged delegates to Publisher.Publish with
// ConflictSkip, so the Publisher's content-dedupe logic
// (hash comparison) replaces the legacy Uploader.UploadFileIfChanged
// (filename-based lookup + MD5 comparison). The skipped bool and
// UploadResultDTO fields are derived from PublishResult.Action +
// PublishResult.FileID/PublishResult.WebViewLink. Group + Subject
// ARE propagated on this surface (the canonical YouTubeClipPath
// path-builder reads them) — the prior GetOrCreateFolder call
// established the channel-level folder context, and this per-file
// call attaches the video-level identity on top.
type YouTubePublisherDriveAdapter struct {
	publisher delivery.Publisher
	log       *zap.Logger
}

// NewYouTubePublisherDriveAdapter returns the canonical adapter.
// pub must be non-nil (the caller — build_bundles_domain.go —
// asserts this). The legacy `drive.Admin` parameter was RETIRED
// per code-reviewer M1+M2 of `0a7f7995` (Group/Subject silently
// dropped on admin path), and the surface is now Publisher-only.
func NewYouTubePublisherDriveAdapter(pub delivery.Publisher, log *zap.Logger) *YouTubePublisherDriveAdapter {
	return &YouTubePublisherDriveAdapter{publisher: pub, log: log}
}

// Compile-time assertion: adapter satisfies DriveFolderManagerPort.
var _ youtubeports.DriveFolderManagerPort = (*YouTubePublisherDriveAdapter)(nil)

func (a *YouTubePublisherDriveAdapter) GetOrCreateFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	// Thread the request-local parent folder into ParentFolderID so
	// the channel folder is created under the caller-selected Drive root.
	// Without this, the destination registry root wins and the clip
	// subfolder drifts into the default catalog tree.
	if a.publisher == nil {
		return parentFolderID, fmt.Errorf("YouTubePublisherDriveAdapter.GetOrCreateFolder: publisher not wired")
	}
	rootOverride := ""
	if parentFolderID != "" {
		rootOverride = parentFolderID
	}
	folderID, err := a.publisher.ResolveFolder(ctx, delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		// ResolveFolder requires both logical path segments when a parent
		// override is supplied. Keep subtitle artifacts in a dedicated
		// namespace, with one leaf folder per clip.
		Group:          "youtube_subtitles",
		Subject:        channelName,
		ParentFolderID: rootOverride,
	})
	if err != nil {
		return parentFolderID, fmt.Errorf("YouTubePublisherDriveAdapter.GetOrCreateFolder: %w", err)
	}
	return folderID, nil
}

func (a *YouTubePublisherDriveAdapter) UploadFileIfChanged(ctx context.Context, localPath, folderID, filename, group, subject string) (*youtubeports.UploadResultDTO, bool, error) {
	// Thread the resolved folder into DestinationFolderID so the
	// publisher writes directly INTO the payload-selected Drive folder.
	// DestinationFolderID is the canonical application-layer leaf
	// (resolveDestination returns it verbatim without consulting the
	// registry, path builders, or catalog) — ParentFolderID would
	// re-run the DestinationYouTubeClip path builder and drift uploads
	// into a `youtube_uncategorized/<video_id>` subfolder under the
	// selected root.
	if a.publisher == nil {
		return nil, false, fmt.Errorf("YouTubePublisherDriveAdapter.UploadFileIfChanged: publisher not wired")
	}
	destFolderID := strings.TrimSpace(folderID)
	result, err := a.publisher.Publish(ctx, delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   localPath,
		Filename:    filename,
		Group:       group,
		Subject:     subject,
		// Thread the resolved folder into DestinationFolderID so the
		// publisher writes directly into the payload-selected Drive
		// folder (leaf semantics, no path-builder subfolder).
		DestinationFolderID: destFolderID,
		ConflictPolicy:      delivery.ConflictSkip,
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
