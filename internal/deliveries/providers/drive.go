package providers

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/deliveries"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

// DriveProvider delivers artifacts to Google Drive.
// Uses ArtifactReader for content access — never touches LocalPath directly.
type DriveProvider struct {
	uploader *drive.Uploader
	log      *zap.Logger
}

// NewDriveProvider creates a new Drive delivery provider.
func NewDriveProvider(uploader *drive.Uploader, log *zap.Logger) *DriveProvider {
	if log == nil {
		log = zap.NewNop()
	}
	return &DriveProvider{uploader: uploader, log: log}
}

// Name returns "drive".
func (p *DriveProvider) Name() string { return "drive" }

// Deliver uploads the artifact to Google Drive via the ArtifactReader.
// Returns *deliveries.Result per the deliveries.Provider contract (pointer-typed
// so streaming providers can re-use the same backing allocation).
func (p *DriveProvider) Deliver(ctx context.Context, artifact deliveries.ArtifactDescriptor, content deliveries.ArtifactReader, dest deliveries.DeliveryDestination) (*deliveries.Result, error) {
	if p.uploader == nil {
		return nil, fmt.Errorf("drive uploader not configured")
	}

	// Open artifact stream from BlobStore
	reader, err := content.Open(ctx, artifact.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("drive: open artifact: %w", err)
	}
	defer reader.Close()

	// Write to temp file for Drive upload (Drive API requires filesystem path)
	tmpFile, err := os.CreateTemp("", "dlv-drive-*")
	if err != nil {
		return nil, fmt.Errorf("drive: create temp: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.ReadFrom(reader); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("drive: read artifact: %w", err)
	}
	tmpFile.Close()

	filename := fmt.Sprintf("artifact_%s", artifact.SHA256[:12])
	folderID := "" // uses configured root

	result, err := p.uploader.UploadFile(ctx, tmpFile.Name(), folderID, filename)
	if err != nil {
		p.log.Warn("drive delivery failed",
			zap.String("artifact_id", artifact.ArtifactID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("drive: upload: %w", err)
	}

	p.log.Info("drive delivery complete",
		zap.String("artifact_id", artifact.ArtifactID),
		zap.String("file_id", result.FileID),
		zap.String("drive_link", result.WebViewLink),
	)

	return &deliveries.Result{
		RemoteID:  result.FileID,
		RemoteURL: result.WebViewLink,
	}, nil
}

// ClassifyError categorizes Drive errors for retry.
func (p *DriveProvider) ClassifyError(err error) deliveries.FailureClass {
	if err == nil {
		return deliveries.FailureTemporary
	}
	errStr := err.Error()
	for _, sub := range []string{"oauth2", "token", "unauthorized", "403"} {
		if strings.Contains(errStr, sub) {
			return deliveries.FailureAuth
		}
	}
	for _, sub := range []string{"not found", "folder", "invalid"} {
		if strings.Contains(errStr, sub) {
			return deliveries.FailurePermanent
		}
	}
	return deliveries.FailureTemporary
}

var _ deliveries.Provider = (*DriveProvider)(nil)
