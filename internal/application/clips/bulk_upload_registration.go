// Package clips (bulk_upload_registration) — Step "register" of the
// per-clip pipeline: build canonical *asset.Asset from candidate +
// PublishResult + fileHash, then route media_assets UPSERT through
// mutations.AssetMutationDispatcher (QDRANT-002 atomicity: media_assets +
// outbox_events INSERT in one tx).
//
// Fail-closed: nil dispatcher surfaces mutations.ErrDispatcherUnavailable;
// nil pubRes after publish returns typed error to caller.
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
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// registerClip builds the canonical *asset.Asset and routes its persistence
// through the canonical dispatcher so the media_assets UPSERT and outbox
// INSERT share one transaction.
//
// Transcript capped at 200000 bytes on clean_transcript (transcript_truncated=true marker so downstream enrichment flags lossy-compression). Manifest youtube_video_id/url + tags propagate via SetMetadataString/Tags.
func registerClip(
	ctx context.Context,
	dispatcher mutations.AssetMutationDispatcher,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
	cand clipCandidate,
	pubRes *delivery.PublishResult,
	fileHash string,
	targetFolderID string,
	log *zap.Logger,
) error {
	if dispatcher == nil {
		return fmt.Errorf("bulk upload dispatcher not configured: %w", mutations.ErrDispatcherUnavailable)
	}
	if pubRes == nil {
		return fmt.Errorf("registerClip: pubRes must be non-nil (publishClip must run before register)")
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
	clip.SetLegacyFileMD5(fileHash)
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
	clip.SetDriveLink(pubRes.WebViewLink)
	clip.SetDownloadLink(pubRes.DownloadLink)
	clip.SetDriveFileID(pubRes.FileID)
	if cand.Transcript != "" {
		const maxLen = 200000
		if len(cand.Transcript) > maxLen {
			clip.SetCleanTranscript(cand.Transcript[:maxLen])
			if clip.Metadata == nil {
				clip.Metadata = make(map[string]any)
			}
			clip.Metadata["transcript_truncated"] = true
		} else {
			clip.SetCleanTranscript(cand.Transcript)
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
