package clips

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ReprocessUseCase re-downloads, re-processes, and re-uploads a clip.
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): dispatcher
// field added so the use case's media_assets UPSERT (post-reprocess)
// routes through the canonical outbox+tx writer (QDRANT-002 atomicity
// invariant). Strict fail-closed: nil dispatcher surfaces explicitly
// via mutations.ErrDispatcherUnavailable wrap, NOT as a half-written
// asset row that would orphan Qdrant. See
// internal/app/registry_adapters.go::newMutationsDispatcherAdapter.
type ReprocessUseCase struct {
	assetRepo     detail.Repository
	processor     detail.Processor
	dispatcher    mutations.AssetMutationDispatcher
	clipsFolderID string
	remoteReader  RemoteAssetReader
}

// RemoteAssetReader is the minimal read port needed to stage a Drive-backed
// clip for processing. A Drive URL is a valid canonical source; a local path
// is only an execution-time staging detail and is not persisted as identity.
// clip_drive reprocess ALWAYS re-stages from Drive (the persisted local_path
// is the previous derived output, never the source), so this port is required
// for every clip_drive reprocess.
type RemoteAssetReader interface {
	DownloadFile(context.Context, string) (io.ReadCloser, string, error)
}

// NewReprocessUseCase constructs the use case.
//
// PR 7 (June 2026): added `dispatcher` as the 3rd positional arg
// (after proc) so media_assets writes enforce the canonical outbox
// path. Composition-root pre-rejection lives in the wiring site
// (internal/api/assets/clips/handler.go NewHandler) which surfaces
// a configure-time error if dispatcher is nil.
func NewReprocessUseCase(repo detail.Repository, proc detail.Processor, dispatcher mutations.AssetMutationDispatcher, clipsFolderID string) *ReprocessUseCase {
	return &ReprocessUseCase{assetRepo: repo, processor: proc, dispatcher: dispatcher, clipsFolderID: clipsFolderID}
}

// SetRemoteAssetReader wires the canonical remote read port. It is optional
// for legacy sources, but required when a clip is Drive-backed without a
// persisted local rendition.
func (uc *ReprocessUseCase) SetRemoteAssetReader(reader RemoteAssetReader) {
	if uc != nil {
		uc.remoteReader = reader
	}
}

// ReprocessRequest contains the input for reprocessing a clip.
type ReprocessRequest struct {
	ClipID      string `json:"clip_id"`
	Source      string `json:"source"`
	Force       bool   `json:"force"`
	UploadDrive bool   `json:"upload_drive"`
	Normalize   *bool  `json:"normalize"`
}

// ReprocessResult contains the output after reprocessing.
type ReprocessResult struct {
	ClipID        string `json:"clip_id"`
	Source        string `json:"source"`
	Status        string `json:"status"`
	LocalPath     string `json:"local_path"`
	LegacyFileMD5 string `json:"legacy_file_md5"`
	DriveLink     string `json:"drive_link"`
	DownloadLink  string `json:"download_link"`
	ProcessedAt   string `json:"processed_at"`
}

// Execute reprocesses the clip and returns the result.
func (uc *ReprocessUseCase) Execute(ctx context.Context, req ReprocessRequest) (*ReprocessResult, error) {
	if uc.assetRepo == nil {
		return nil, fmt.Errorf("asset repository not available")
	}
	if uc.processor == nil {
		return nil, fmt.Errorf("media processor not configured")
	}

	clip, err := uc.assetRepo.Get(ctx, req.ClipID)
	if err != nil {
		return nil, fmt.Errorf("clip not found: %w", err)
	}
	if clip == nil {
		return nil, fmt.Errorf("clip not found")
	}

	// force=false (reprocess contract fix, July 2026): reuse the
	// existing derived rendition when the clip already has a valid
	// one on disk — no download, no re-encode, no upload. This makes
	// the flag actually control whether reprocess re-runs the
	// pipeline instead of being silently ignored.
	if !req.Force && uc.hasExistingRendition(clip) {
		return &ReprocessResult{
			ClipID:        req.ClipID,
			Source:        req.Source,
			Status:        "processed",
			LocalPath:     clip.LocalPath(),
			LegacyFileMD5: clip.LegacyFileMD5(),
			DriveLink:     clip.DriveLink(),
			DownloadLink:  clip.DownloadLink(),
			ProcessedAt:   timeutil.FormatRFC3339(clip.UpdatedAt),
		}, nil
	}

	// Build ProcessInput from clip data
	sourceURL := reprocessSourceURL(clip, req.Source)
	folderID := clip.FolderID()
	if folderID == "" && (req.Source == "youtube" || req.Source == "youtube-manual") {
		folderID = uc.clipsFolderID
	}
	processInput := &detail.ProcessInput{
		ID:   clip.ID,
		Name: clip.Name,
		// Reprocess contract fix (August 2026): thread the clip's
		// canonical filename (yt_<videoID>_<start>_<end>_<policy>_<slug>.mp4)
		// so the processor uploads under the SAME Drive name as the
		// original upload. ConflictOverwrite then finds the existing file
		// and updates it in place instead of creating a fresh orphan on
		// every reprocess. Empty (legacy rows) → processor falls back to
		// the SafeName+ID default.
		Filename:  clip.Filename,
		SourceURL: sourceURL,
		FolderID:  folderID,
		Duration:  int(clip.Duration.Milliseconds()),
		KeepAudio: true,
		// Reprocess contract fix (July 2026): the normalize and
		// upload_drive flags now actually control the pipeline.
		// normalize=false skips the ffmpeg normalize (mux/copy only);
		// upload_drive=false skips the canonical Drive publish.
		Normalize:   req.Normalize,
		SkipPublish: !req.UploadDrive,
		// Folder alignment (August 2026): route YouTube clip uploads
		// through the canonical DestinationYouTubeClip registry policy
		// so the file lands in clips/{group}/{video_id} (the clip's real
		// folder) instead of the Artlist destination + stale ParentFolderID
		// that orphaned every folder-mismatch reprocess.
		Destination: string(delivery.DestinationYouTubeClip),
		Group:       reprocessGroup(clip),
		Subject:     reprocessSubject(clip),
		Metadata: map[string]any{
			"source": req.Source,
			"tags":   clip.Tags,
		},
	}
	var stagedPath string
	if req.Source == "clip_drive" {
		if uc.remoteReader == nil {
			return nil, fmt.Errorf("clip %s has no local rendition and remote reader is not configured", req.ClipID)
		}
		driveID := strings.TrimSpace(clip.DriveFileID())
		if driveID == "" {
			return nil, fmt.Errorf("clip %s has no Drive file id", req.ClipID)
		}
		body, contentType, err := uc.remoteReader.DownloadFile(ctx, driveID)
		if err != nil {
			return nil, fmt.Errorf("download Drive clip %s: %w", req.ClipID, err)
		}
		defer body.Close()
		ext := ".mp4"
		if strings.Contains(strings.ToLower(contentType), "quicktime") {
			ext = ".mov"
		}
		file, err := os.CreateTemp("", "pipelinegen-clip-stage-*-"+ext)
		if err != nil {
			return nil, fmt.Errorf("create Drive staging file: %w", err)
		}
		stagedPath = file.Name()
		defer os.Remove(stagedPath)
		if _, err := io.Copy(file, body); err != nil {
			file.Close()
			return nil, fmt.Errorf("stage Drive clip %s: %w", req.ClipID, err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close Drive staging file: %w", err)
		}
		processInput.SourceURL = ""
		processInput.LocalPath = stagedPath
	}

	result, err := uc.processor.Process(ctx, processInput)
	if err != nil {
		return nil, fmt.Errorf("reprocess failed: %w", err)
	}

	// Update clip with result
	clip.SetLocalPath(result.LocalPath)
	clip.SetLegacyFileMD5(result.LegacyFileMD5)
	if result.DriveFileID != "" {
		// F2.8 parity (reprocess contract fix, August 2026): the canonical
		// Drive identity pointer must track the newly published rendition.
		// Pre-fix the use case updated drive_link/download_link but left
		// drive_file_id pointing at the stale previous file — a DB↔Drive
		// divergence that made the next clip_drive reprocess re-download
		// the old source and orphaned every fresh upload.
		clip.SetDriveFileID(result.DriveFileID)
	}
	if result.DriveLink != "" {
		clip.SetDriveLink(result.DriveLink)
	}
	if result.DownloadLink != "" {
		clip.SetDownloadLink(result.DownloadLink)
	}
	if result.MD5 != "" {
		// Drive-returned md5Checksum, distinct from the local SHA carried
		// by LegacyFileMD5 (the QDRANT-002 source_version/content_hash). Stored
		// under its own metadata key so it never clobbers the local hash.
		clip.SetMetadataString("md5", result.MD5)
	}
	if result.PublishAction != "" {
		// Mirror reupload/upload: record the canonical Publisher action
		// (created | updated | skipped | renamed) for post-publish audit.
		clip.SetMetadataString("publish_action", result.PublishAction)
	}
	clip.UpdatedAt = time.Now()

	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): route the
	// media_assets UPSERT through the canonical mutations.AssetMutationDispatcher
	// so the QDRANT-002 atomicity invariant (media_assets UPSERT + outbox_events
	// INSERT in one tx) applies uniformly to reprocess flows.
	//
	// Strict fail-closed: nil dispatcher surfaces an explicit error
	// (the POST /clips/{id}/reprocess call returns 503-equivalent),
	// NOT a half-written asset row. contentHash is the post-reprocess
	// fingerprint from result.LegacyFileMD5; mirrors the v1 supersede-gate
	// semantics (QDRANT-002 item F: source_version on index.requested.v1).
	if uc.dispatcher == nil {
		return nil, fmt.Errorf("reprocess dispatcher not configured (QDRANT-asset-mutation isolation required): %w", mutations.ErrDispatcherUnavailable)
	}
	if err := uc.dispatcher.EnqueueAndIndex(ctx, clip, result.LegacyFileMD5); err != nil {
		return nil, fmt.Errorf("dispatcher enqueue: %w", err)
	}

	return &ReprocessResult{
		ClipID:        req.ClipID,
		Source:        req.Source,
		Status:        result.Status,
		LocalPath:     result.LocalPath,
		LegacyFileMD5: result.LegacyFileMD5,
		DriveLink:     result.DriveLink,
		DownloadLink:  result.DownloadLink,
		ProcessedAt:   timeutil.FormatRFC3339(time.Now()),
	}, nil
}

// hasExistingRendition reports whether the clip already carries a
// valid derived rendition on disk (non-empty file hash + existing,
// non-empty local file). Used by the force=false short-circuit.
func (uc *ReprocessUseCase) hasExistingRendition(clip *asset.Asset) bool {
	if clip == nil {
		return false
	}
	if clip.LegacyFileMD5() == "" {
		return false
	}
	local := clip.LocalPath()
	if local == "" {
		return false
	}
	info, err := os.Stat(local)
	return err == nil && info.Size() > 0
}

// reprocessGroup derives the canonical YouTubeClip group path segment
// (the channel/folder name) for the reprocess upload. Order of preference:
//
//  1. folder_path — the real Drive folder name. media_assets.group
//     historically defaulted to the literal "group" and category is empty
//     on legacy YouTube rows, so folder_path is the only reliable channel
//     name for those clips.
//  2. group — the typed column, skipped when it holds the legacy "group"
//     placeholder.
//  3. category.
func reprocessGroup(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	if v := strings.TrimSpace(clip.FolderPath()); v != "" {
		return v
	}
	if v := strings.TrimSpace(clip.Group); v != "" && v != "group" {
		return v
	}
	return strings.TrimSpace(clip.Category)
}

// reprocessSubject derives the canonical YouTubeClip subject path segment
// (the YouTube video ID) for the reprocess upload. Prefers the persisted
// source_video_id, then youtube_video_id, then the first segment of the
// canonical clip ID (yt_<videoID>_<start>_<end>_<policy>[slug]).
func reprocessSubject(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	if v := strings.TrimSpace(clip.MetadataSourceVideoID()); v != "" {
		return v
	}
	if v := strings.TrimSpace(clip.YouTubeVideoID()); v != "" {
		return v
	}
	id := strings.TrimPrefix(strings.TrimSpace(clip.ID), "yt_")
	if cut := strings.IndexByte(id, '_'); cut > 0 {
		return id[:cut]
	}
	return ""
}

// reprocessSourceURL prefers the canonical source URL, then derives the
// YouTube URL from legacy asset IDs that predate source_url persistence.
// The old Drive URL is intentionally not used as a reprocess source: it can
// point at the corrupted derived clip that this operation is repairing.
func reprocessSourceURL(clip *asset.Asset, source string) string {
	if clip == nil {
		return ""
	}
	if v := strings.TrimSpace(clip.SourceURL); v != "" && !isDerivedDriveURL(v, source) {
		return v
	}
	for _, v := range []string{
		clip.MetadataSourceURL(),
		clip.YouTubeURL(),
		clip.GetMetadataString("url"), // legacy alias retained for pre-convergence rows
	} {
		if v = strings.TrimSpace(v); v != "" && !isDerivedDriveURL(v, source) {
			return v
		}
	}
	if source == "youtube" || source == "youtube-manual" {
		id := strings.TrimPrefix(strings.TrimSpace(clip.ID), "yt_")
		if cut := strings.LastIndexByte(id, '_'); cut > 0 && isAssetHashSuffix(id[cut+1:]) {
			videoID := id[:cut]
			return "https://www.youtube.com/watch?v=" + videoID
		}
	}
	return ""
}

func isAssetHashSuffix(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func isDerivedDriveURL(raw, source string) bool {
	return (source == "youtube" || source == "youtube-manual") &&
		strings.Contains(strings.ToLower(raw), "drive.google.com")
}
