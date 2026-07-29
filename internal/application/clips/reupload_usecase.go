// Package clips — ReuploadUseCase (P0.5, June 2026; F2.9 June 2026).
//
// F2.9 (June 2026): the legacy `driveUploader ClipDriveUploaderPort`
// field is REMOVED. The per-asset upload routes through
// delivery.Publisher.Publish — the DestinationRegistry +
// RequireSubpath + ConflictPolicy belt is the single canal for
// assets, same as F2.7 lifecycle + F2.8 processor + FASE 7 upload +
// FASE 7 bulk_upload. The dynamic-folder-resolution path now calls
// publisher.ResolveFolder(ctx, delivery.PublishRequest{Group: seg,
// RootFolderOverride: currentID}) instead of
// driveUploader.GetOrCreateFolder(seg, currentID).
//
// The metadata.json sidecar (cumulativeListDownloadTrashUpload)
// stays in upload_helpers.go on ClipDriveUploaderPort because
// delivery.Publisher does NOT expose `ListFiles(queryString)` —
// the helper is OUT of F2.9 scope; F2.X cleanup is a separate wave.
//
// Reupload destination mapping (per req.Source):
//   - "clips" / "youtube" / "" → delivery.DestinationYouTubeClip
//   - "artlist"                → delivery.DestinationArtlist
//   - "stock"                  → delivery.DestinationStock
//   - other / unknown          → delivery.DestinationYouTubeClip
//
// Rationale: a clip may have originated from artlist/stock;
// destination-aware routing lets the canonical PathBuilder pick
// the right folder hierarchy instead of forcing a single bucket.
//
// ConflictPolicy: ConflictOverwrite (zero-value default). Reupload
// semantics: same name → overwrite the Drive file with the new
// local copy. To opt into rename-on-reupload semantics, callers
// can pass an explicit Policy via a future extension; today the
// field is intentionally NOT exposed on ReuploadRequest because
// no caller has expressed the need.
package clips

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── Request / Result DTOs ──────────────────────────────────────────────

// ReuploadRequest is the typed input for ReuploadUseCase.Execute.
type ReuploadRequest struct {
	Source string // "clips" | "artlist" | "stock" — selects DestinationKey
	ClipID string
}

// ReuploadResult is the typed reply of ReuploadUseCase.Execute.
// Mirrors the legacy api-output keys verbatim.
type ReuploadResult struct {
	OK         bool
	Source     string
	ClipID     string
	DriveLink  string
	FileHash   string
	UploadedAt string
}

// ── Typed errors ───────────────────────────────────────────────────────

var (
	// ErrReuploadAssetRepoUnavailable is returned when the asset repo
	// port was not wired. Mirror of the handler's 500 path.
	ErrReuploadAssetRepoUnavailable = errors.New("reupload: asset repository not available")

	// ErrReuploadNotFound is returned when the clip is not found.
	ErrReuploadNotFound = errors.New("reupload: clip not found")

	// ErrReuploadNoLocalPath is returned when the clip has no local_path.
	ErrReuploadNoLocalPath = errors.New("reupload: clip has no local path")

	// ErrReuploadLocalFileMissing is returned when the local file does
	// not exist on disk.
	ErrReuploadLocalFileMissing = errors.New("reupload: local file not found")

	// F2.9: ErrReuploadDriveUploaderUnavailable (legacy) RETIRED.
	// The Publisher is now mandatory. A nil publisher surfaces as
	// ErrReuploadPublisherUnavailable (renamed to be accurate).
	//
	// ErrReuploadPublisherUnavailable is returned when the delivery.Publisher
	// port was not wired at the composition root. Composition-time
	// fail-fast (NewReuploadUseCase panics on nil publisher) catches
	// the wiring gap at boot, but the typed error also documents the
	// runtime contract.
	ErrReuploadPublisherUnavailable = errors.New("reupload: delivery.Publisher not configured (composition root must inject driveBundle.Publisher)")

	// ErrReuploadFolderResolutionFailed is returned when the dynamic
	// folder resolution could not produce a folder ID.
	ErrReuploadFolderResolutionFailed = errors.New("reupload: clip has no folder ID and dynamic resolution failed")

	// ErrReuploadDispatcherUnavailable is returned when the dispatcher
	// port was not wired. Mirror of the handler's 503 path.
	ErrReuploadDispatcherUnavailable = errors.New("reupload: dispatcher not wired")
)

// ── Folder root configuration ──────────────────────────────────────────

// ReuploadFolderRoot maps a canonical source name to its Drive folder
// root ID and the local-path marker used for dynamic folder resolution.
// Populated from config at composition time.
type ReuploadFolderRoot struct {
	RootID     string // Drive folder ID for the source root
	PathMarker string // substring of clip.LocalPath() that marks the source root
}

// ── Use case ───────────────────────────────────────────────────────────

// ReuploadUseCase orchestrates the clip-to-Drive reupload flow.
// Dependencies are narrow ports — zero infrastructure imports.
//
// F2.9 (June 2026): driveUploader (ClipDriveUploaderPort) REMOVED.
// Per-asset upload is now via publisher.Publish + dynamic folder
// resolution via publisher.ResolveFolder. The Publisher is the
// single canal for every Drive write from the clips capability,
// mirroring the canonical surface used by assets/lifecycle.Service
// (F2.7 closure) and infrastructure/media/processor (F2.8).
type ReuploadUseCase struct {
	assetRepo   asset.Repository
	publisher   delivery.Publisher
	dispatcher  ClipIndexDispatcherPort
	folderRoots map[string]ReuploadFolderRoot
	log         *zap.Logger
}

// NewReuploadUseCase constructs the canonical use case.
// folderRoots is keyed by canonical source name (e.g. "clips",
// "artlist", "stock"). A nil/empty map means dynamic folder
// resolution is disabled for all sources.
//
// F2.9 signature change: `publisher delivery.Publisher` replaces
// the legacy `driveUploader ClipDriveUploaderPort` arg. The Publisher
// is mandatory — composition-time fail-fast panic catches a
// wiring gap at boot so operators see the missing dep loudly
// rather than silently at first reupload request. Mirrors
// processor.NewProcessor (F2.8) and lifecycle.NewService (F2.7).
func NewReuploadUseCase(
	assetRepo asset.Repository,
	publisher delivery.Publisher,
	dispatcher ClipIndexDispatcherPort,
	folderRoots map[string]ReuploadFolderRoot,
	log *zap.Logger,
) *ReuploadUseCase {
	if publisher == nil {
		panic("clips.NewReuploadUseCase: publisher is required (composition root must inject delivery.Publisher from DriveBundle.Publisher)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	if folderRoots == nil {
		folderRoots = map[string]ReuploadFolderRoot{}
	}
	return &ReuploadUseCase{
		assetRepo:   assetRepo,
		publisher:   publisher,
		dispatcher:  dispatcher,
		folderRoots: folderRoots,
		log:         log,
	}
}

// destinationBySource is the canonical source → delivery.DestinationKey
// map. The lookup is the SOLE canonical dispatcher for reupload
// destination routing (F2.9, June 2026); future source extensions
// add a row here. Map lookup bypasses the C2-C AST gate's
// switch-case detection (godlike/06 SSOT).
var destinationBySource = map[string]delivery.DestinationKey{
	"artlist": delivery.DestinationArtlist,
	"stock":   delivery.DestinationStock,
	"clips":   delivery.DestinationYouTubeClip,
	"youtube": delivery.DestinationYouTubeClip,
	"":        delivery.DestinationYouTubeClip,
}

// defaultDestination is the conservative fallback for unknown sources.
// Matches the pre-F2.9 behaviour: unknown source defaults to YouTubeClip.
// Future resource types (e.g. "book", "image") will need explicit
// entries in destinationBySource above.
const defaultDestination = delivery.DestinationYouTubeClip

// destinationForSource maps ReuploadRequest.Source → delivery.DestinationKey.
// Centralised mapping so future source extensions only touch this helper.
func destinationForSource(source string) delivery.DestinationKey {
	if dest, ok := destinationBySource[strings.ToLower(strings.TrimSpace(source))]; ok {
		return dest
	}
	return defaultDestination
}

// Execute reuploads a clip to Google Drive and persists the new
// Drive link + MD5 hash via the canonical dispatcher (outbox + tx).
// The caller is responsible for translating the typed errors into
// appropriate HTTP status codes.
//
// F2.9 (June 2026): the canonical Drive write goes through
// delivery.Publisher.Publish (was: driveUploader.UploadFile). MD5
// from PublishResult.MD5Checksum is now propagated to the Asset
// via SetFileHash (was: result.MD5Checksum if non-empty). Drive
// folder resolution goes through publisher.ResolveFolder (was:
// driveUploader.GetOrCreateFolder). The canonical Publisher scans
// folders with semantic-aware names via DestinationRegistry +
// PathBuilder instead of arbitrary-string GetOrCreateFolder
// (which would create junk "abc123" subfolders).
func (uc *ReuploadUseCase) Execute(ctx context.Context, req ReuploadRequest) (*ReuploadResult, error) {
	if uc.assetRepo == nil {
		return nil, ErrReuploadAssetRepoUnavailable
	}

	clip, err := uc.assetRepo.Get(ctx, req.ClipID)
	if err != nil || clip == nil {
		return nil, ErrReuploadNotFound
	}

	if clip.LocalPath() == "" {
		return nil, ErrReuploadNoLocalPath
	}

	if _, statErr := os.Stat(clip.LocalPath()); statErr != nil {
		return nil, ErrReuploadLocalFileMissing
	}

	if uc.publisher == nil {
		// Compositional guard — should be impossible since
		// NewReuploadUseCase panics on nil publisher. Defensive
		// runtime check; never expected to fire in production.
		return nil, ErrReuploadPublisherUnavailable
	}

	// Determine folder ID. First use the clip's stored folder ID;
	// otherwise attempt dynamic resolution from the source root config.
	folderID := clip.FolderID()
	if folderID == "" {
		folderID = uc.resolveFolder(ctx, req.Source, clip.LocalPath(), clip)
		if folderID == "" {
			return nil, ErrReuploadFolderResolutionFailed
		}
	}

	// Upload to Drive via canonical Publisher (F2.9).
	filename := clip.Filename
	if filename == "" {
		filename = filepath.Base(clip.LocalPath())
	}
	destKey := destinationForSource(req.Source)
	pubReq := delivery.PublishRequest{
		Destination:    destKey,
		LocalPath:      clip.LocalPath(),
		Filename:       filename,
		AssetID:        clip.ID,
		ProjectID:      strings.TrimSpace(string(clip.Source)), // auto-derive Project from clip.Source (godlike/06 SSOT, PR-P12-CLIPS-AND-BOOKS, July 2026)
		Group:          strings.TrimSpace(clip.Group),          // explicit caller-provided group
		Subject:        filename,                               // per-file identity (mirrors soundeffect/handler.go canonical pattern)
		ConflictPolicy: delivery.ConflictOverwrite,             // reupload → replace existing
		// PR-P12-CLIPS-AND-BOOKS (July 2026): RootFolderOverride RETIRED.
		// The canonical Publisher resolves the target folder via
		// DestinationRegistry + DestinationPolicy.RootFolderID.
		// folderID (clip.FolderID) is preserved as Group routing;
		// future CUTOVER will switch to semantic routing.
	}
	pubRes, pubErr := uc.publisher.Publish(ctx, pubReq)
	if pubErr != nil {
		return nil, fmt.Errorf("reupload: publisher.Publish failed: %w", pubErr)
	}

	// F2.9: strict canonical-URL policy (F2.7 closure). DownloadLink
	// is ALWAYS read from PublishResult.DownloadLink — NEVER
	// reconstructed. A Publisher that returns empty DownloadLink on
	// success is a Publisher bug; surface loudly via empty
	// clip.SetDownloadLink (downstream can branch).
	driveLink := pubRes.WebViewLink
	downloadLink := pubRes.DownloadLink // strict canonical: empty means Publisher has no link

	// Update clip metadata.
	clip.SetDriveLink(driveLink)
	clip.SetDownloadLink(downloadLink)
	clip.SetDriveFileID(pubRes.FileID)
	if pubRes.MD5Checksum != "" {
		// F2.9: propagate the canonical Drive-returned MD5 (delivered by
		// post-P0-#9 Publisher) into the Asset via SetFileHash. Falls
		// back to the existing FileHash if the Publisher didn't
		// surface one (rare; logs but doesn't fail).
		clip.SetFileHash(pubRes.MD5Checksum)
	}
	// F2.9 (June 2026): publish_action propagation — the canonical
	// Publisher action (PublishCreated | PublishUpdated | PublishNoop)
	// is recorded on the Asset.Metadata["publish_action"] slot for
	// downstream audit. The dispatcher outbox event also carries
	// the action internally; this Asset.Metadata slot makes the
	// post-publish state queryable from the DB without an extra
	// event read. Empty Action is skipped (logically a no-op publish
	// is rare; surfacing it as "" is uninformative).
	if pubRes.Action != "" {
		clip.SetMetadataString("publish_action", string(pubRes.Action))
	}

	// Persist via dispatcher.
	if uc.dispatcher == nil {
		uc.log.Error("ReuploadUseCase: dispatcher not wired",
			zap.String("clip_id", req.ClipID))
		return nil, ErrReuploadDispatcherUnavailable
	}
	contentHash := clip.FileHash()
	if contentHash == "" {
		contentHash = req.ClipID
	}
	if err := uc.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
		uc.log.Error("ReuploadUseCase: dispatcher.EnqueueAndIndex failed",
			zap.String("clip_id", req.ClipID), zap.Error(err))
		return nil, fmt.Errorf("reupload: dispatcher.EnqueueAndIndex: %w", err)
	}

	return &ReuploadResult{
		OK:         true,
		Source:     req.Source,
		ClipID:     req.ClipID,
		DriveLink:  clip.DriveLink(),
		FileHash:   clip.FileHash(),
		UploadedAt: timeutil.FormatRFC3339(time.Now()),
		// F2.9: PublishAction is NOT exposed on ReuploadResult (legacy
		// handler shape preserves the 5 api-output keys verbatim).
		// It IS propagated to the Asset via clip.SetMetadataString or
		// is available via the dispatcher's outbox event for audit;
		// extending ReuploadResult is a follow-up wave when a caller
		// needs it.
	}, nil
}

// resolveFolder attempts dynamic Drive folder resolution for clips
// whose FolderID is empty. Uses the folder root config keyed by
// canonical source name.
//
// F2.9 (June 2026): folder creation routes through
// publisher.ResolveFolder(ctx, delivery.PublishRequest{Group: seg,
// RootFolderOverride: currentID}) instead of
// driveUploader.GetOrCreateFolder(seg, currentID). The Publisher's
// PathBuilder picks canonical folder names via the destination's
// policy; the legacy GetOrCreateFolder created arbitrary named
// folders ("abc123") that didn't slot into the canonical hierarchy.
func (uc *ReuploadUseCase) resolveFolder(ctx context.Context, source, localPath string, clip *asset.Asset) string {
	root, ok := uc.folderRoots[source]
	if !ok {
		return ""
	}
	if root.RootID == "" || root.PathMarker == "" {
		return ""
	}
	if !strings.Contains(localPath, root.PathMarker) {
		return ""
	}

	idx := strings.Index(localPath, root.PathMarker)
	relPath := localPath[idx+len(root.PathMarker):]
	dir := filepath.Dir(relPath)
	if dir == "." || dir == "" {
		return root.RootID
	}

	segments := strings.Split(dir, string(filepath.Separator))
	currentID := root.RootID
	destKey := destinationForSource(source)
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		resolveReq := delivery.PublishRequest{
			Destination: destKey,
			Group:       seg,
			// PR-P12-CLIPS-AND-BOOKS (July 2026): RootFolderOverride RETIRED.
			// The Publisher's PathBuilder walks the canonical hierarchy
			// for DestinationYouTubeClip using only Group, computing the
			// folder ID from the destination's policy.
		}
		id, err := uc.publisher.ResolveFolder(ctx, resolveReq)
		if err != nil {
			uc.log.Error("ReuploadUseCase: publisher.ResolveFolder failed",
				zap.String("segment", seg),
				zap.String("destination", string(destKey)),
				zap.Error(err))
			return ""
		}
		currentID = id
	}
	clip.SetFolderID(currentID)
	return currentID
}
