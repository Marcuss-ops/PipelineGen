// Package clips (bulk_upload_clip_pub) — Step "clip-pub" of the
// per-clip pipeline in the "bulk_upload_youtube_clips" bg job.
//
// P1.7 (July 2026): extracted from
// internal/application/clips/bulk_upload_worker.go as part of the
// 7-file worker-pipeline split.
//
// Builds the Drive-side filename (subdir preferred → display name →
// raw name) and multi-line description (helper from
// bulk_upload_helpers.go::buildBulkDriveDescription) and routes the
// .mp4 through the canonical delivery.Publisher.Publish path
// (mandatory since P0.1).
//
// Publishes ONLY the .mp4 itself. Sidecar files (clip_manifest.json
// + transcript.txt) are handled by the sidecar-pub section
// (bulk_upload_sidecar_pub.go).
//
// No new abstractions — top-level helper function returning the
// canonical *delivery.PublishResult. The caller (HandleJob's
// processOneClip stitch) reads the fields it needs for downstream
// registration + sidecar-pub.
package clips

import (
	"context"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// publishClip publishes the .mp4 candidate to Google Drive via the
// canonical Publisher port.
//
// On success returns the *delivery.PublishResult populated by the
// Publisher. On failure returns an error wrapping the publish stage.
//
// Caller-side responsibilities (in processOneClip):
//   - bump uploaded.Add(1) on success
//   - thread targetFolderID = pubRes.FolderID to registerClip + publishSidecars
//   - log the publish action (pubRes.Action) for audit
//
// Skip-upload gate (payload.SkipUpload) is the caller's responsibility;
// this function does NOT check the gate.
func publishClip(
	ctx context.Context,
	publisher ClipPublisherPort,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
	cand clipCandidate,
	fileHash string,
	log *zap.Logger,
) (*delivery.PublishResult, error) {
	// Build Drive filename: use subdir (actor name) if available,
	// else clip name.
	driveName := ""
	if cand.Subdir != "" && cand.Subdir != "." {
		driveName = sanitiseDriveName(filepath.Base(cand.Subdir))
	}
	if driveName == "" {
		driveName = sanitiseDriveName(cand.DisplayName())
	}
	if driveName == "" {
		driveName = cand.Name
	}
	driveFilename := driveName + ".mp4"
	driveDesc := buildBulkDriveDescription(cand, fileHash, *payload)

	// PR-13 (July 2026): layout policy is server-controlled. The
	// PipelineGen Drive layout always mirrors local subdirs, so
	// pubGroup is the candidate's subdir whenever present.
	pubGroup := ""
	if cand.Subdir != "" && cand.Subdir != "." {
		pubGroup = cand.Subdir
	}

	pubReq := delivery.PublishRequest{
		Destination: delivery.DestinationYouTubeClip,
		LocalPath:   cand.LocalPath,
		Filename:    driveFilename,
		Description: driveDesc,
		Group:       pubGroup,
		ProjectID:   payload.DriveFolderID,
		// PR-P12-CLIPS-AND-BOOKS (July 2026): RootFolderOverride RETIRED.
		// The caller's explicit DriveFolderID is now routed as ProjectID
		// so the canonical Publisher's DestinationYouTubeClip root +
		// PathBuilder construct the target folder hierarchy.
	}
	pubRes, err := publisher.Publish(ctx, pubReq)
	if err != nil {
		if log != nil {
			log.Error("clip-pub publish failed",
				zap.String("path", cand.LocalPath),
				zap.Error(err))
		}
		return nil, fmt.Errorf("drive publish: %w", err)
	}
	return pubRes, nil
}
