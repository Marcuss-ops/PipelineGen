// Package clips (bulk_upload_registration) — Steps "register" of
// the per-clip pipeline: build the canonical *asset.Asset from the
// scanned candidate + (optionally) the publish result + file hash,
// then route the media_assets UPSERT through the canonical
// mutations.AssetMutationDispatcher port so the QDRANT-002
// atomicity invariant (media_assets UPSERT + outbox_events INSERT in
// one tx) applies uniformly to bulk-uploaded clips.
//
// P1.7 (July 2026): extracted from
// internal/application/clips/bulk_upload_worker.go as part of the
// 7-file worker-pipeline split.
//
// Strict fail-closed (PR 7, June 2026 — codex/qdrant-app-writers-fail-closed):
// a nil dispatcher returns mutations.ErrDispatcherUnavailable wrapped
// with context so the work's failure is operator-visible via the job
// outcome, NOT as a half-written asset row that would orphan a
// Qdrant upsert. contentHash is the MD5 from scan-pipeline.MD5File;
// mirrors the v1 supersede-gate semantics (QDRANT-002 item F:
// source_version on index.requested.v1).
//
// No new abstractions — top-level helper function; the
// *asset.Asset build + Set* calls are unchanged from the
// pre-split inline code.
package clips

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// registerClip builds the canonical *asset.Asset from the scanned
// candidate + (optionally) the publish result + file hash, then
// routes the asset + contentHash through the canonical
// mutations.AssetMutationDispatcher so the media_assets UPSERT and
// the outbox INSERT share a single transaction.
//
// Pre-split semantics preserved:
//   - pubRes may be nil (SkipUpload gate): the asset.Asset is
//     still built and persisted (local-only path), Drive-side
//     fields are simply left empty.
//   - targetFolderID is the resolved folder id from the
//     .mp4 publish (pubRes.FolderID if available, else
//     payload.DriveFolderID for SkipUpload).
//   - The transcript is truncated to 200000 bytes on the
//     clean_transcript slot; transcript_truncated=true is set on
//     truncation so downstream enrichment can flag the
//     lossy-compression.
//   - The manifest's youtube_video_id / youtube_url + tags are
//     copied onto the asset via SetMetadataString / append.
func registerClip(
	ctx context.Context,
	dispatcher mutations.AssetMutationDispatcher,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
	cand clipCandidate,
	pubRes *delivery.PublishResult, // nil if SkipUpload
	fileHash string,
	targetFolderID string,
	log *zap.Logger,
) error {
	if dispatcher == nil {
		return fmt.Errorf("bulk upload dispatcher not configured (QDRANT-asset-mutation isolation required): %w", mutations.ErrDispatcherUnavailable)
	}

	now := time.Now().UTC()
	source := payload.Source
	if source == "" {
		source = "youtube-local"
	}
	category := payload.Category
	if category == "" && cand.Subdir != "" && cand.Subdir != "." {
		category = strings.SplitN(cand.Subdir, "/", 2)[0]
	}

	clip := &asset.Asset{
		ID:             buildBulkClipID(cand, fileHash),
		Name:           cand.DisplayName(),
		Filename:       filepath.Base(cand.LocalPath),
		Source:         asset.Source(source),
		Category:       category,
		MediaType:      asset.MediaType("video"),
		SearchText:     deriveSearchText(cand),
		LifecycleState: asset.StateActive,
		Duration:       time.Duration(extractIntFromManifest(cand.Manifest, "duration_sec")) * time.Second,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	clip.SetLocalPath(cand.LocalPath)
	clip.SetFileHash(fileHash)
	clip.SetFolderID(targetFolderID)
	clip.SetFolderPath(cand.Subdir)
	if cand.Manifest != nil {
		if v, ok := cand.Manifest["youtube_video_id"].(string); ok && v != "" {
			clip.SetMetadataString("youtube_video_id", v)
		} else if v, ok := cand.Manifest["youtube_id"].(string); ok && v != "" {
			clip.SetMetadataString("youtube_video_id", v)
		}
		if v, ok := cand.Manifest["youtube_url"].(string); ok && v != "" {
			clip.SetMetadataString("youtube_url", v)
		} else if v, ok := cand.Manifest["url"].(string); ok && v != "" {
			clip.SetMetadataString("youtube_url", v)
		}
	}
	if v, ok := cand.Manifest["tags"].([]any); ok {
		for _, t := range v {
			if s, ok := t.(string); ok && s != "" {
				clip.Tags = append(clip.Tags, s)
			}
		}
	}
	if pubRes != nil {
		clip.SetDriveLink(pubRes.WebViewLink)
		clip.SetDownloadLink(pubRes.DownloadLink)
		clip.SetDriveFileID(pubRes.FileID)
	}
	if cand.Transcript != "" {
		if clip.Metadata == nil {
			clip.Metadata = make(map[string]any)
		}
		const maxLen = 200000
		if len(cand.Transcript) > maxLen {
			clip.Metadata["clean_transcript"] = cand.Transcript[:maxLen]
			clip.Metadata["transcript_truncated"] = true
		} else {
			clip.Metadata["clean_transcript"] = cand.Transcript
		}
	}

	if err := dispatcher.EnqueueAndIndex(ctx, clip, fileHash); err != nil {
		if log != nil {
			log.Error("clip-registration dispatcher.EnqueueAndIndex failed",
				zap.String("clip_id", clip.ID),
				zap.Error(err))
		}
		return fmt.Errorf("dispatcher enqueue: %w", err)
	}
	return nil
}

