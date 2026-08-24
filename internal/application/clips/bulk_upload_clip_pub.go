// Package clips (bulk_upload_clip_pub) — Step "clip-pub" of the per-clip
// pipeline: publish the .mp4 via canonical delivery.Publisher and return
// the PublishResult. Sidecars (clip_manifest.json + transcript.txt) are
// handled by the sidecar-pub section; this function publishes ONLY the .mp4.
//
// pubGroup mirrors cand.Subdir to keep the PipelineGen Drive layout aligned
// with the local subfolder hierarchy.
package clips

import (
	"context"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
)

// publishClip publishes the .mp4 candidate to Google Drive via the canonical
// Publisher port. Caller (processOneClip) threads targetFolderID
// (pubRes.FolderID) to registerClip + publishSidecars;
// sidecars are OUT of scope here (separate file).
func publishClip(
	ctx context.Context,
	publisher ClipPublisherPort,
	payload *appjobs.BulkUploadYouTubeClipsPayload,
	cand clipCandidate,
	fileHash string,
	log *zap.Logger,
) (*delivery.PublishResult, error) {
	// Drive filename: subdir (actor name) > display name > raw name.
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
