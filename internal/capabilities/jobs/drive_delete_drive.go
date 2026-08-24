package jobs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"go.uber.org/zap"
	"google.golang.org/api/googleapi"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

var driveLinkFileIDPattern = regexp.MustCompile(`/d/([A-Za-z0-9_-]+)`)

// extractDriveFileID resolves the canonical Drive identifier from the asset's
// explicit field first and then its Drive-facing links.
func extractDriveFileID(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	if id := clip.DriveFileID(); id != "" {
		return id
	}
	for _, link := range []string{clip.DriveLink(), clip.DownloadLink()} {
		if link == "" {
			continue
		}
		match := driveLinkFileIDPattern.FindStringSubmatch(link)
		if len(match) >= 2 && match[1] != "" {
			return match[1]
		}
	}
	return ""
}

// performDriveDelete owns the external Drive side effect. A typed Google 404 is
// folded into idempotent success; all other errors remain retryable.
func (h *DriveDeleteHandler) performDriveDelete(
	ctx context.Context,
	clip *asset.Asset,
	req driveDeleteRequestV1,
	reqLog []zap.Field,
	log *zap.Logger,
) error {
	fileID := extractDriveFileID(clip)
	if h.drive == nil {
		return errors.New("drive_delete: drive port not wired (production wiring must supply DriveDeleter)")
	}
	if fileID == "" {
		log.Info("drive_delete: no Drive fileID — skipping Drive side-effect, advancing directly", reqLog...)
		return nil
	}

	log.Info("drive_delete: invoking Drive API", append(reqLog, zap.String("file_id", fileID))...)
	var driveErr error
	if req.Permanently {
		driveErr = h.drive.Delete(ctx, fileID)
		if driveErr != nil && isDriveNotFoundError(driveErr) {
			log.Info("drive_delete: Drive.Delete returned 404, treating as idempotent success",
				append(reqLog, zap.String("file_id", fileID))...,
			)
			driveErr = nil
		}
	} else {
		driveErr = h.drive.Trash(ctx, fileID)
		if driveErr != nil && isDriveNotFoundError(driveErr) {
			log.Info("drive_delete: Drive.Trash returned 404, treating as idempotent success",
				append(reqLog, zap.String("file_id", fileID))...,
			)
			driveErr = nil
		}
	}
	if driveErr != nil {
		log.Warn("drive_delete: Drive API failed (retryable — row stays in DRIVE_DELETE_PENDING)",
			append(reqLog, zap.Error(driveErr))...,
		)
		return fmt.Errorf("drive_delete Drive API for %s: %w", req.AssetID, driveErr)
	}
	return nil
}

func isDriveNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var googleErr *googleapi.Error
	if !errors.As(err, &googleErr) {
		return false
	}
	return googleErr.Code == http.StatusNotFound
}
