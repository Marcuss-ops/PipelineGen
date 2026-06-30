// Package drive — publisher.go (FASE 3, June 2026; P0 #1 fix June 2026)
//
// Publisher is the concrete implementation of delivery.Publisher. It is the
// ONLY point in the system that:
//  1. Resolves a DestinationKey to a root folder + path segments via the
//     DestinationRegistry.
//  2. Creates nested Drive folders via FolderManager.EnsureFolder.
//  3. Uploads files via Uploader.PutFile (conflict-aware, P0 #1).
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

// PutAction describes what the uploader actually did on Drive. It is
// the typed replacement for unspecified "did we overwrite or create?"
// return values — callers can branch on Action to update their audit
// trail, emit events, or skip DB writes for skips. Mirrors the
// project convention of typed string enums (delivery.ConflictPolicy,
// delivery.DeliveryStatus, asset.LifecycleState).
type PutAction string

const (
	PutActionCreated PutAction = "created" // fresh Drive file (no existing match)
	PutActionUpdated PutAction = "updated" // existing Drive file updated in place
	PutActionSkipped PutAction = "skipped" // existing Drive file preserved (ConflictSkip; no upload performed)
	PutActionRenamed PutAction = "renamed" // new Drive file with conflict-rename suffix
)

// PutFileRequest is the single low-level op the Publisher must route
// conflict-aware uploads through. Carrying ConflictPolicy at this
// seam eliminates the dead-enum failure mode (P0 #1): every caller
// MUST pick Overwrite/Skip/Rename explicitly. Zero-value policy
// resolves to delivery.ConflictOverwrite to match legacy behaviour.
type PutFileRequest struct {
	LocalPath      string
	FolderID       string
	Filename       string
	Description    string // optional; empty means "no description"
	ConflictPolicy delivery.ConflictPolicy // zero = ConflictOverwrite (legacy default)
}

// PutFileResult is the structured return value. Action tells callers
// what actually happened on Drive; all metadata fields are present in
// every successful case (including the skip branch, where the
// existing file's metadata is returned so the caller does not have
// to re-issue FindFileByName).
type PutFileResult struct {
	FileID       string
	WebViewLink  string
	DownloadLink string
	MD5Checksum  string
	Action       PutAction
}

// FileUploaderPort is the narrow port for uploading files to Drive.
// Satisfied by *Uploader.PutFile (see uploader_put.go).
//
// P0 #1 (June 2026): the legacy UploadFileWithDescription method was
// removed from this port. The PutFile method carries the
// ConflictPolicy at the seam so callers cannot bypass it. Raw callers
// that need the unconditional-overwrite shape should depend on
// drive.Admin.UploadFileWithDescription (kept for cmd/admin and
// similar raw contexts).
type FileUploaderPort interface {
	PutFile(ctx context.Context, req PutFileRequest) (*PutFileResult, error)
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

	// Allow callers to override the root folder (backward compat for
	// script generation jobs that pass an explicit FolderID).
	rootFolderID := policy.RootFolderID
	if override := strings.TrimSpace(req.RootFolderOverride); override != "" {
		rootFolderID = override
	}

	if rootFolderID == "" {
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
	folderID := rootFolderID
	if len(segments) > 0 {
		folderID, err = p.folders.EnsureFolder(ctx, rootFolderID, segments...)
		if err != nil {
			return nil, fmt.Errorf("delivery: resolve drive path for %q: %w", req.Destination, err)
		}
	}

	// Step 5: Normalise the filename.
	filename, err := normalizeFilename(req.Filename)
	if err != nil {
		return nil, fmt.Errorf("delivery: normalise filename: %w", err)
	}

	// Step 6: Upload the file (conflict-aware, P0 #1 fix).
	// req.ConflictPolicy flows through PutFileRequest so the uploader
	// picks created/updated/skipped/renamed based on the explicit
	// policy rather than silently overwriting.
	result, err := p.files.PutFile(ctx, PutFileRequest{
		LocalPath:      req.LocalPath,
		FolderID:       folderID,
		Filename:       filename,
		Description:    req.Description,
		ConflictPolicy: req.ConflictPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("delivery: publish to %q: %w", req.Destination, err)
	}

	p.log.Info("delivery: file published",
		zap.String("destination", string(req.Destination)),
		zap.String("folder_id", folderID),
		zap.String("file_id", result.FileID),
		zap.String("action", string(result.Action)),
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

// ResolveFolder resolves the Drive folder for a destination without uploading.
// Reuses steps 1-4 of Publish (resolve policy, build path, ensure folder).
func (p *Publisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	policy, err := p.registry.Resolve(req.Destination)
	if err != nil {
		return "", err
	}

	rootFolderID := policy.RootFolderID
	if override := strings.TrimSpace(req.RootFolderOverride); override != "" {
		rootFolderID = override
	}

	if rootFolderID == "" {
		return "", fmt.Errorf("delivery: destination %q has no configured root folder", req.Destination)
	}

	segments, err := policy.PathBuilder(req)
	if err != nil {
		return "", fmt.Errorf("delivery: build path for %q: %w", req.Destination, err)
	}

	folderID := rootFolderID
	if len(segments) > 0 {
		folderID, err = p.folders.EnsureFolder(ctx, rootFolderID, segments...)
		if err != nil {
			return "", fmt.Errorf("delivery: resolve drive path for %q: %w", req.Destination, err)
		}
	}

	p.log.Info("delivery: folder resolved",
		zap.String("destination", string(req.Destination)),
		zap.String("folder_id", folderID),
		zap.Strings("segments", segments),
	)

	return folderID, nil
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
