// Package drive — publisher.go (FASE 3, June 2026)
//
// Publisher is the concrete implementation of delivery.Publisher. It is the
// ONLY point in the system that:
//  1. Resolves a DestinationKey to a root folder + path segments via the
//     DestinationRegistry.
//  2. Creates nested Drive folders via FolderManager.EnsureFolder.
//  3. Uploads files via Uploader.UploadFileWithDescription.
//
// All other code paths (YouTube, Books, Images, SFX, Clips, Artlist, Stock,
// Voiceover, Script) MUST go through delivery.Publisher.Publish rather than
// calling FolderManager or Uploader directly.
//
// The Publisher is constructed once at composition time
// (internal/app/build_bundles_drive.go) and injected into the DriveBundle.
package drive

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// FolderManagerPort is the narrow port for creating nested Drive folders.
// Satisfied by *DriveFolderManagerAdapter.EnsureFolder.
type FolderManagerPort interface {
	EnsureFolder(ctx context.Context, parent string, segments ...string) (string, error)
}

// FileUploaderPort is the narrow port for uploading files to Drive.
// Satisfied by *Uploader.UploadFileWithDescription.
type FileUploaderPort interface {
	UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*UploadResult, error)
}

// Publisher implements delivery.Publisher. It resolves the destination,
// builds the folder hierarchy, normalises the filename, and uploads.
type Publisher struct {
	registry *delivery.DestinationRegistry
	folders  FolderManagerPort
	files    FileUploaderPort
	log      *zap.Logger
}

// Compile-time assertion: Publisher satisfies delivery.Publisher.
var _ delivery.Publisher = (*Publisher)(nil)

// NewPublisher constructs the canonical Drive publisher.
func NewPublisher(
	registry *delivery.DestinationRegistry,
	folders FolderManagerPort,
	files FileUploaderPort,
	log *zap.Logger,
) *Publisher {
	if log == nil {
		log = zap.NewNop()
	}
	return &Publisher{
		registry: registry,
		folders:  folders,
		files:    files,
		log:      log,
	}
}

// Publish resolves the destination, builds the folder path, creates folders,
// normalises the filename, and uploads the file. This is the single canal
// for all Drive writes.
func (p *Publisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	// Step 1: Resolve the destination policy.
	policy, err := p.registry.Resolve(req.Destination)
	if err != nil {
		return nil, err
	}

	if policy.RootFolderID == "" {
		return nil, fmt.Errorf(
			"delivery: destination %q has no configured root folder",
			req.Destination,
		)
	}

	// Step 2: Build the path segments.
	segments, err := policy.PathBuilder(req)
	if err != nil {
		return nil, fmt.Errorf("delivery: build path for %q: %w", req.Destination, err)
	}

	// Step 3: Enforce RequireSubpath.
	if policy.RequireSubpath && len(segments) == 0 {
		return nil, fmt.Errorf(
			"delivery: direct upload into root %q is forbidden for destination %q",
			policy.RootFolderID, req.Destination,
		)
	}

	// Step 4: Resolve or create the folder hierarchy.
	folderID := policy.RootFolderID
	if len(segments) > 0 {
		folderID, err = p.folders.EnsureFolder(ctx, policy.RootFolderID, segments...)
		if err != nil {
			return nil, fmt.Errorf("delivery: resolve drive path for %q: %w", req.Destination, err)
		}
	}

	// Step 5: Normalise the filename.
	filename, err := normalizeFilename(req.Filename)
	if err != nil {
		return nil, fmt.Errorf("delivery: normalise filename: %w", err)
	}

	// Step 6: Upload the file.
	result, err := p.files.UploadFileWithDescription(
		ctx,
		req.LocalPath,
		folderID,
		filename,
		req.Description,
	)
	if err != nil {
		return nil, fmt.Errorf("delivery: upload to %q: %w", req.Destination, err)
	}

	p.log.Info("delivery: file published",
		zap.String("destination", string(req.Destination)),
		zap.String("folder_id", folderID),
		zap.String("file_id", result.FileID),
		zap.Strings("segments", segments),
	)

	return &delivery.PublishResult{
		FileID:       result.FileID,
		WebViewLink:  result.WebViewLink,
		FolderID:     folderID,
		Destination:  req.Destination,
		PathSegments: segments,
	}, nil
}

// normalizeFilename sanitises a filename for Drive upload.
// Uses textutil.SanitizeFilename which strips path traversal, NUL bytes,
// and other dangerous characters. Rejects empty results.
func normalizeFilename(name string) (string, error) {
	clean := textutil.SanitizeFilename(name)
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return "", fmt.Errorf("filename is empty after sanitisation")
	}
	// Ensure the filename has a reasonable extension check — at minimum
	// it should have a base name.
	if filepath.Base(clean) == "." || filepath.Base(clean) == ".." {
		return "", fmt.Errorf("filename %q resolves to a reserved path component", name)
	}
	return clean, nil
}
