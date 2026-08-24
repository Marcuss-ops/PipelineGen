// Package images (application/images) — service_storage.go holds
// the storage/ingest/sync methods on Service. Per PR-IMG-SPLIT-4
// (July 2026), these are the operational storage methods that
// delegate to the Store or Meta sub-services.
//
// Golden rule: storage methods never touch Gen (AI generation) or
// retrieved search logic directly — they operate on existing assets.
package workflow

import (
	"context"
	"io"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// IngestImage ingests raw image bytes into the storage system.
func (s *Service) IngestImage(ctx context.Context, slug, style, genID string, data io.Reader, filename, sourceURL, description string, tags []string, skipDrive, skipMetadata bool) (*asset.ImageAsset, error) {
	return s.Store.IngestImage(ctx, slug, style, genID, data, filename, sourceURL, description, tags, skipDrive, skipMetadata)
}

// UploadToStyleDrive uploads an image asset to its style-specific
// Drive folder.
func (s *Service) UploadToStyleDrive(ctx context.Context, imageAsset *asset.ImageAsset, style string) (string, string, error) {
	return s.Store.UploadToStyleDrive(ctx, imageAsset, style)
}

// SyncFromDrive synchronizes image assets from Google Drive.
func (s *Service) SyncFromDrive(ctx context.Context) error { return s.Store.SyncFromDrive(ctx) }

// FormatDriveLink formats a Drive file ID into a viewable link.
func (s *Service) FormatDriveLink(id string) string { return s.Store.FormatDriveLink(id) }
