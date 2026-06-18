package providers

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"velox/go-master/internal/artifacts"
	"velox/go-master/internal/deliveries"
	"velox/go-master/internal/upload/drive"
)

// DriveProvider delivers artifacts to Google Drive.
type DriveProvider struct {
	uploader *drive.Uploader
	artifacts *artifacts.Service
	log     *zap.Logger
}

// NewDriveProvider creates a new Drive delivery provider.
func NewDriveProvider(uploader *drive.Uploader, artifactSvc *artifacts.Service, log *zap.Logger) *DriveProvider {
	if log == nil {
		log = zap.NewNop()
	}
	return &DriveProvider{
		uploader:  uploader,
		artifacts: artifactSvc,
		log:       log,
	}
}

// Name returns "drive".
func (p *DriveProvider) Name() string { return "drive" }

// Deliver uploads the artifact to Google Drive. The artifact's SHA-256
// is used as the filename to ensure content-addressed naming.
// Returns the Drive file ID and web view link on success.
func (p *DriveProvider) Deliver(ctx context.Context, req deliveries.Request) (deliveries.Result, error) {
	if p.uploader == nil {
		return deliveries.Result{}, fmt.Errorf("drive uploader not configured")
	}

	// If we have a local path, use it directly
	localPath := req.LocalPath
	if localPath == "" && req.OpenReader != nil {
		// Stream from BlobStore to a temp file for Drive upload
		reader, err := req.OpenReader(ctx)
		if err != nil {
			return deliveries.Result{}, fmt.Errorf("drive: open artifact: %w", err)
		}
		defer reader.Close()

		tmpFile, err := os.CreateTemp("", "dlv-drive-*")
		if err != nil {
			return deliveries.Result{}, fmt.Errorf("drive: create temp: %w", err)
		}
		defer os.Remove(tmpFile.Name())

		if _, err := tmpFile.ReadFrom(reader); err != nil {
			tmpFile.Close()
			return deliveries.Result{}, fmt.Errorf("drive: read artifact: %w", err)
		}
		tmpFile.Close()
		localPath = tmpFile.Name()
	}

	if localPath == "" {
		return deliveries.Result{}, fmt.Errorf("drive: no content available for %s", req.ArtifactID)
	}

	// Use the root folder from config (passed as "" — uploader uses configured default)
	filename := fmt.Sprintf("artifact_%s", req.SHA256[:12])
	// Try to get Drive folder from targetID or use root
	folderID := "" // will use configured root

	result, err := p.uploader.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		p.log.Warn("drive delivery failed", zap.String("artifact_id", req.ArtifactID), zap.Error(err))
		return deliveries.Result{}, fmt.Errorf("drive: upload: %w", err)
	}

	p.log.Info("drive delivery complete",
		zap.String("artifact_id", req.ArtifactID),
		zap.String("file_id", result.FileID),
		zap.String("drive_link", result.WebViewLink),
	)

	return deliveries.Result{
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
	// Auth errors
	for _, sub := range []string{"oauth2", "token", "unauthorized", "403"} {
		if strings.Contains(errStr, sub) {
			return deliveries.FailureAuth
		}
	}
	// Permanent config errors
	for _, sub := range []string{"not found", "folder", "invalid"} {
		if strings.Contains(errStr, sub) {
			return deliveries.FailurePermanent
		}
	}
	// Default: retryable
	return deliveries.FailureTemporary
}

// Compile-time check
var _ deliveries.Provider = (*DriveProvider)(nil)
