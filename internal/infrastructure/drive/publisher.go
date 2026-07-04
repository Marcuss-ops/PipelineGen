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
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── Composition-time fail-fast sentinels (P0 #3, June 2026) ────────────────
//
// NewPublisher used to nil-deref any unset Pattern 0 port at the first
// downstream call (`p.registry.Resolve(...)` from Publish, etc.). The
// composition root (internal/app/build_bundles_drive.go) had no way to
// distinguish "deps wired correctly" from "deps wired to nil because
// Drive auth failed at boot". Pre-P0 #3, a `driveClient == nil` Drive
// block at composition would still produce a `var publisher delivery.Publisher
// = drive.NewPublisher(registry, folderMgr, driveUploader, log)` literal
// at build_bundles_drive.go:85 — the typed-NIL interface trap from
// godlike/06 would hand callers a non-nil interface holding a nil
// concrete, whose first call site exploded at runtime without a clean
// composition-time indicator.
//
// The three sentinels below close that gap. Each is exported so callers
// (cmd/admin/, internal/app/composition.go, future health-barrier checks)
// can errors.Is / errors.As against the verbatim sentinel — the same
// pattern godlike/07 §"Compatibility test" uses for the pre-existing
// outbox typed-port contract.

// ErrMissingDestinationRegistry is the fail-fast sentinel when the
// composition root hands a nil DestinationRegistry to NewPublisher.
// Per AGENTS.md Pattern 8 (thin API transport only) + godlike/06
// "one owner per fact", the registry MUST be non-nil at construction
// time so downstream code paths (`p.registry.Resolve(...)` from Publish,
// ResolveFolder, resolveDestination) can dereference the pointer without
// producing a runtime nil-panic at first call site.
var ErrMissingDestinationRegistry = errors.New("drive: NewPublisher: DestinationRegistry dependency is required (composition-time fail-fast)")

// ErrMissingFolderManager is the fail-fast sentinel when the
// FolderManagerPort dependency is nil. Per AGENTS.md Pattern 0
// (port abstraction layer), the FolderManagerPort is the canonical
// compile-time boundary between Publisher and DriveFolderManagerAdapter;
// a nil port at construction indicates a ComposeRoot misconfiguration
// (typed-nil interface trap that would otherwise surface only at first
// EnsureFolder call site).
var ErrMissingFolderManager = errors.New("drive: NewPublisher: FolderManagerPort dependency is required (composition-time fail-fast)")

// ErrMissingFileUploader is the fail-fast sentinel when the
// FileUploaderPort dependency is nil. Per AGENTS.md Pattern 0, the
// FileUploaderPort is the canonical boundary for FileUploaderPort.PutFile
// (the P0 #1 conflict-aware uploader seam); a nil port at construction
// indicates a ComposeRoot misconfiguration.
var ErrMissingFileUploader = errors.New("drive: NewPublisher: FileUploaderPort dependency is required (composition-time fail-fast)")

// FolderManagerPort is the narrow port for creating nested Drive folders.
// Satisfied by *DriveFolderManagerAdapter.EnsureFolder.
//
// P1.3 (July 2026): adds ProbeFolderAccess(ctx, rootID) for the
// composition-time StartupDriveRootsValidator. ProbeFolderAccess
// verifies that a configured root folder is reachable on Drive WITHOUT
// creating any folder — it uses Files.Get with retry-with-jitter and
// is the canonical fail-closed surface for the registry-driven
// startup validation. The probe is side-effect-free (read-only) so
// even validators that loop across dozens of destinations do not
// produce spurious Drive-side folder creation.
type FolderManagerPort interface {
	EnsureFolder(ctx context.Context, parent string, segments ...string) (string, error)
	ProbeFolderAccess(ctx context.Context, rootID string) error
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
//
// P0.6 (July 2026): IdempotencyKey replaces folderID+filename as the
// authoritative identity for conflict detection. When non-empty, the
// uploader uses Drive appProperties lookup instead of filename match.
// ContentHash and SourceVersion are carried for idempotency-key
// derivation and future audit surfaces.
type PutFileRequest struct {
	LocalPath      string
	FolderID       string
	Filename       string
	Description    string                   // optional; empty means "no description"
	ConflictPolicy delivery.ConflictPolicy   // zero = ConflictOverwrite (legacy default)
	IdempotencyKey string                   // P0.6: Drive appProperties key for conflict detection
	ContentHash    string                   // P0.6: hex-encoded SHA-256 of file content
	SourceVersion  int64                    // P0.6: logical source version
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

// NewPublisher constructs the canonical Drive publisher. Fails-fast at
// composition time on any nil Pattern 0 dependency (the three sentinels
// ErrMissingDestinationRegistry, ErrMissingFolderManager,
// ErrMissingFileUploader are returned verbatim so composition-root callers
// can errors.Is them and emit a typed-NIL-safe audit message).
//
// The nil-check order is: registry → folders → files (typed-discovery order:
// the resolve helper p.registry.Resolve(...) is the FIRST downstream call
// site a Publish request exercises, so the registry check must come first
// to surface the most-likely-to-fail dependency at the top of the error
// stream). `log` separately tolerates a nil value (defaults to zap.NewNop)
// because adapter constructors typically receive a logger from the same
// parent root and a nil logger shouldn't block a fail-fast path.
func NewPublisher(
	registry *delivery.DestinationRegistry,
	folders FolderManagerPort,
	files FileUploaderPort,
	log *zap.Logger,
) (*Publisher, error) {
	if registry == nil {
		return nil, ErrMissingDestinationRegistry
	}
	if folders == nil {
		return nil, ErrMissingFolderManager
	}
	if files == nil {
		return nil, ErrMissingFileUploader
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Publisher{
		registry: registry,
		folders:  folders,
		files:    files,
		log:      log,
	}, nil
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
	//
	// PR-VO-SUBFOLDER (July 2026): run the PathBuilder unconditionally so
	// callers with an explicit RootFolderOverride still get the canonical
	// subpath structure (e.g. voiceover: {project}/{language}). When the
	// caller supplies a target folder but the PathBuilder fails because
	// metadata (Group / Subject / Language, etc.) is missing, gracefully
	// fall back to a direct upload into that root folder rather than
	// failing the whole publish — backward-compatible with callers that
	// opt out of the subpath structure by passing a pre-resolved root.
	var segments []string
	pathBuilt := false
	if segments, err = policy.PathBuilder(req); err == nil {
		pathBuilt = true
	} else if req.RootFolderOverride != "" {
		p.log.Warn(
			"delivery: PathBuilder failed with RootFolderOverride set; uploading directly into root folder",
			zap.String("destination", string(req.Destination)),
			zap.String("root_folder_id", rootFolderID),
			zap.Error(err),
		)
		segments = nil
		err = nil
	} else {
		return nil, fmt.Errorf("delivery: build path for %q: %w", req.Destination, err)
	}

	// Step 5: RequireSubpath enforcement (SYMMETRIC across callers).
	// Before P0 #2, only Publish checked RequireSubpath; ResolveFolder
	// could resolve a folder that Publish would have rejected. Now both
	// paths go through this helper so the surface is consistent. With
	// the PR-VO-SUBFOLDER change above, RequireSubpath is enforced ONLY
	// when PathBuilder ran successfully (pathBuilt=true) — the
	// explicit-root-fallback path is intentionally opted out (the caller
	// already provided their own target folder, so demanding a subpath
	// would contradict the caller intent).
	if policy.RequireSubpath && len(segments) == 0 && pathBuilt {
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
//
// P1.1 (July 2026): when req.ConflictPolicy == 0 (the "caller didn't
// pick a policy" path), Publish consults the registry's per-destination
// default and threads that value into Step 6. Explicit
// req.ConflictPolicy values (Skip / Overwrite / Rename) are honoured
// verbatim. The legacy zero == ConflictOverwrite silent fallback is
// gone; immutable destinations now default to Skip so a freshly-added
// caller cannot accidentally overwriteDrive files under the same name.
func (p *Publisher) Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	// Step 0 (P1.1): registry-driven ConflictPolicy default.
	//
	// When the caller did not pick a policy — i.e.
	// req.ConflictPolicy == delivery.ConflictPolicyUnset (the
	// iota-zero sentinel) — consult the registry so the per-destination
	// safety contract (ConflictSkip for immutable assets,
	// ConflictOverwrite for regenerable outputs) is honoured.
	// Explicit req.ConflictPolicy values (ConflictOverwrite /
	// ConflictSkip / ConflictRename) are honoured verbatim — explicit
	// caller choice always wins over the registry default.
	//
	// The lookup happens BEFORE resolveDestination so an unknown
	// destination surfaces as the typed registry error rather than
	// leaking into the resolution pipeline as a silent fallback —
	// the registry error is preserved verbatim.
	if req.ConflictPolicy == delivery.ConflictPolicyUnset {
		policy, pErr := p.registry.Resolve(req.Destination)
		if pErr != nil {
			return nil, pErr
		}
		req.ConflictPolicy = policy.ConflictPolicy
	}

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
	// req.ConflictPolicy — after Step 0 above either the caller's
	// explicit choice or the registry's per-destination default — flows
	// through to PutFileRequest so the uploader picks
	// created/updated/skipped/renamed based on the explicit policy
	// rather than silently overwriting.
	result, err := p.files.PutFile(ctx, PutFileRequest{
		LocalPath:      req.LocalPath,
		FolderID:       resolved.FolderID,
		Filename:       filename,
		Description:    req.Description,
		ConflictPolicy: req.ConflictPolicy,
		IdempotencyKey: req.IdempotencyKey,
		ContentHash:    req.ContentHash,
		SourceVersion:  req.SourceVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("delivery: publish to %q: %w", req.Destination, err)
	}

	// ── Translate drive.PutAction → delivery.PublishAction ───────────────────
	// The conversion lives here (and is exposed as a private method
	// `(*Publisher).actionFor` below) rather than in delivery/types.go
	// because delivery MUST NOT import the drive package (Pattern 0
	// layering — the application layer has zero outward dependencies
	// on infrastructure). drive.PutAction is in the same package as
	// Publisher, so the conversion stays at the boundary. If a future
	// PutAction constant is added to drive and forgotten here, unknown
	// values fall through to delivery.PublishActionUnknown — a
	// conservative no-op state that downstream callers can detect
	// and refuse to treat as a fresh "created" outcome.
	action := p.actionFor(result.Action)

	// FolderPath is the slash-joined form of PathSegments. Empty when
	// PathSegments is empty (the root-folder case). PathSegments
	// remains the authoritative ordered view; FolderPath is the
	// derived single-string surface for auditing and display.
	folderPath := strings.Join(resolved.PathSegments, "/")

	p.log.Info("delivery: file published",
		zap.String("destination", string(req.Destination)),
		zap.String("folder_id", resolved.FolderID),
		zap.String("file_id", result.FileID),
		zap.String("action", string(result.Action)),
		zap.String("publish_action", string(action)),
		zap.Strings("segments", resolved.PathSegments),
		zap.String("folder_path", folderPath),
	)

	return &delivery.PublishResult{
		FileID:       result.FileID,
		WebViewLink:  result.WebViewLink,
		DownloadLink: result.DownloadLink,
		MD5Checksum:  result.MD5Checksum,
		FolderID:     resolved.FolderID,
		FolderPath:   folderPath,
		Destination:  req.Destination,
		PathSegments: resolved.PathSegments,
		Action:       action,
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

// actionFor translates a drive.PutAction (low-level uploader outcome)
// to a delivery.PublishAction (cross-package surface value). This is
// the canonical boundary conversion; tests pin each arm via
// TestPublisher_TranslatePutAction_Table in publisher_test.go so
// adding a future drive.PutAction constant without updating this
// switch surfaces as a failing test, not a silent fall-through to
// PublishActionUnknown in production callsites.
//
// The method is exposed (lowercase; same-package access) rather than
// inlined so the test can call it directly and avoid duplicating the
// switch arm-by-arm. If a future refactor moves the switch elsewhere,
// the test breaks immediately and pin coverage migrates automatically.
func (p *Publisher) actionFor(input PutAction) delivery.PublishAction {
	switch input {
	case PutActionCreated:
		return delivery.PublishActionCreated
	case PutActionUpdated:
		return delivery.PublishActionUpdated
	case PutActionSkipped:
		return delivery.PublishActionSkipped
	case PutActionRenamed:
		return delivery.PublishActionRenamed
	default:
		return delivery.PublishActionUnknown
	}
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
