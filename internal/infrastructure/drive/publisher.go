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

// ResolvedDriveDestination is the outcome of the canonical destination
// resolution pipeline shared by Publish and ResolveFolder. It marks the
// boundary between registration-time resolution (registry + overrides
// + path builder + RequireSubpath enforcement) and Drive-time mutation
// (folder hierarchy creation).
//
// Both Publish AND ResolveFolder MUST go through resolveDestination so
// RequireSubpath is enforced symmetrically across callers. P0 #2 (June
// 2026) identified that ResolveFolder used a near-duplicate of the
// Steps 1-4 block but skipped the RequireSubpath check, allowing a
// caller to "resolve" a folder that Publish would have rejected.
// Centralising the pipeline makes that misroute impossible.
type ResolvedDriveDestination struct {
	// Destination echoes the requested DestinationKey for audit /
	// projection layers (so callers don't need to thread req through
	// alongside the result).
	Destination delivery.DestinationKey
	// RootFolderID is the resolved root folder after RootFolderOverride
	// has been applied. Empty iff registry.RootFolderID is also empty
	// (callers should treat that as an upstream misconfiguration,
	// already rejected by resolveDestination itself).
	RootFolderID string
	// FolderID is the leaf folder ID after FolderManager.EnsureFolder
	// has built the segment hierarchy. If PathSegments was empty,
	// FolderID == RootFolderID; otherwise it is the deepest nested ID.
	FolderID string
	// PathSegments are the resolved segments used to build the
	// hierarchy. Empty iff the resolved policy's RequireSubpath is
	// false; resolveDestination rejects such a state when the policy
	// requires a subpath so downstream code never sees an empty
	// PathSegments when one was contractually required.
	PathSegments []string
}

// resolveDestination executes the canonical destination resolution
// pipeline shared by Publish and ResolveFolder (P0 #2, June 2026).
//
// Steps (canonical order — see ARCHITECTURE.md §6):
//  1. registry.Resolve(req.Destination)
//  2. Apply RootFolderOverride (back-compat escape hatch)
//  3. Reject empty root folder (misconfiguration surface)
//  4. policy.PathBuilder(req)
//  5. RequireSubpath enforcement (was previously only in Publish;
//     extracted here so ResolveFolder gets the same surface)
//  6. FolderManager.EnsureFolder (only if PathSegments is non-empty)
//
// This method does NOT call PutFile — that remains exclusively
// Publisher.Publish's responsibility.
func (p *Publisher) resolveDestination(ctx context.Context, req delivery.PublishRequest) (*ResolvedDriveDestination, error) {
	// Step 1: Registry resolve.
	policy, err := p.registry.Resolve(req.Destination)
	if err != nil {
		return nil, err
	}

	// Step 2: Root override (back-compat for script generation jobs
	// that historically passed an explicit FolderID).
	rootFolderID := policy.RootFolderID
	if override := strings.TrimSpace(req.RootFolderOverride); override != "" {
		rootFolderID = override
	}

	// Step 3: Empty-root rejection.
	if rootFolderID == "" {
		return nil, fmt.Errorf(
			"delivery: destination %q has no configured root folder",
			req.Destination,
		)
	}

	// Step 4: Path builder.
	segments, err := policy.PathBuilder(req)
	if err != nil {
		return nil, fmt.Errorf("delivery: build path for %q: %w", req.Destination, err)
	}

	// Step 5: RequireSubpath enforcement (SYMMETRIC across callers).
	// Before P0 #2, only Publish checked RequireSubpath; ResolveFolder
	// could resolve a folder that Publish would have rejected. Now both
	// paths go through this helper so the surface is consistent.
	if policy.RequireSubpath && len(segments) == 0 {
		return nil, fmt.Errorf(
			"delivery: direct upload into root %q is forbidden for destination %q",
			rootFolderID, req.Destination,
		)
	}

	// Step 6: Folder hierarchy creation.
	folderID := rootFolderID
	if len(segments) > 0 {
		folderID, err = p.folders.EnsureFolder(ctx, rootFolderID, segments...)
		if err != nil {
			return nil, fmt.Errorf("delivery: resolve drive path for %q: %w", req.Destination, err)
		}
	}

	return &ResolvedDriveDestination{
		Destination:  req.Destination,
		RootFolderID: rootFolderID,
		FolderID:     folderID,
		PathSegments: segments,
	}, nil
}

// Publish resolves the destination, builds the folder path, creates folders,
// normalises the filename, and uploads the file. This is the single canal
// for all Drive writes.
//
// Steps 1–4 (registry resolve + root override + empty-root reject +
// path builder + RequireSubpath enforce + EnsureFolder) are delegated
// to resolveDestination so the resolution pipeline is shared with
// ResolveFolder (P0 #2, June 2026). Publish's added responsibilities
// are: Step 5 (filename normalise) and Step 6 (PutFile upload).
func (p *Publisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	// Steps 1–4: resolution pipeline (delegated, P0 #2). The helper
	// enforces RequireSubpath symmetrically across Publish and
	// ResolveFolder, eliminating the previous ResolveFolder bypass.
	resolved, err := p.resolveDestination(ctx, req)
	if err != nil {
		return nil, err
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
		FolderID:       resolved.FolderID,
		Filename:       filename,
		Description:    req.Description,
		ConflictPolicy: req.ConflictPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("delivery: publish to %q: %w", req.Destination, err)
	}

	p.log.Info("delivery: file published",
		zap.String("destination", string(req.Destination)),
		zap.String("folder_id", resolved.FolderID),
		zap.String("file_id", result.FileID),
		zap.String("action", string(result.Action)),
		zap.Strings("segments", resolved.PathSegments),
	)

	return &delivery.PublishResult{
		FileID:       result.FileID,
		WebViewLink:  result.WebViewLink,
		FolderID:     resolved.FolderID,
		Destination:  req.Destination,
		PathSegments: resolved.PathSegments,
	}, nil
}

// ResolveFolder resolves the Drive folder for a destination without uploading.
//
// Delegates to resolveDestination so the resolution pipeline (including
// the RequireSubpath check) is shared with Publish (P0 #2, June 2026).
// Before P0 #2, ResolveFolder skipped the RequireSubpath check and
// could return a root-folder ID that Publish would have rejected —
// callers that delta-resolve-then-publish could observe a folder ID
// upstream of a publish-time rejection with no obvious cause. Now both
// flows go through the same helper, so the surface is identical.
func (p *Publisher) ResolveFolder(ctx context.Context, req delivery.PublishRequest) (string, error) {
	resolved, err := p.resolveDestination(ctx, req)
	if err != nil {
		return "", err
	}

	p.log.Info("delivery: folder resolved",
		zap.String("destination", string(req.Destination)),
		zap.String("folder_id", resolved.FolderID),
		zap.Strings("segments", resolved.PathSegments),
	)

	return resolved.FolderID, nil
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
