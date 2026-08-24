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

	delivery "github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	platformdelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// FolderManagerPort, PutAction, PutFileRequest, PutFileResult, FileUploaderPort,
// CatalogFolderLookup, and composition-time sentinels have moved to
// publisher_types.go (Pattern 5 split, July 2026).
// Publisher implements delivery.Publisher. It resolves the destination,
// builds the folder hierarchy, normalises the filename, and uploads.
type Publisher struct {
	registry      *platformdelivery.DestinationRegistry
	folders       FolderManagerPort
	files         FileUploaderPort
	log           *zap.Logger
	catalogLookup CatalogFolderLookup // optional — nil when catalog not wired
	catalogWriter CatalogFolderWriter // optional — records newly ensured paths
}

// Compile-time assertion: Publisher satisfies delivery.Publisher.
var _ delivery.Publisher = (*Publisher)(nil)

// SetCatalogLookup wires the local catalog lookup port into the Publisher.
// When set, resolveDestination consults the catalog before calling
// FolderManagerPort.EnsureFolder — if a matching entry exists with
// status=active and a non-empty folder_id, the Publisher uses the
// cached folder ID directly (no Drive API call).
//
// Nil-tolerant: passing nil disables catalog lookups. Callers that
// don't have a catalog wired (tests, smoke-env) leave it nil.
func (p *Publisher) SetCatalogLookup(lookup CatalogFolderLookup) {
	p.catalogLookup = lookup
	if writer, ok := lookup.(CatalogFolderWriter); ok {
		p.catalogWriter = writer
	}
}

// SetCatalogWriter wires the local catalog writer. Newly ensured paths are
// persisted after Drive confirms the folder, making the next publish resolve
// from SQLite instead of listing Drive again.
func (p *Publisher) SetCatalogWriter(writer CatalogFolderWriter) {
	p.catalogWriter = writer
}

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
	registry *platformdelivery.DestinationRegistry,
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

// ResolvedDriveDestination has moved to publisher_types.go
// (Pattern 5 split, July 2026).
// resolveDestination lives in publisher_resolve.go.
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
		// An explicit folder is already a resolved destination. Do not
		// consult the registry merely to choose a collision policy; use
		// the conservative immutable-artifact default. Voiceover and
		// other pre-resolved flows must remain independent of configured
		// roots and path builders.
		if strings.TrimSpace(req.DestinationFolderID) != "" {
			req.ConflictPolicy = delivery.ConflictSkip
		} else {
			policy, pErr := p.registry.Resolve(req.Destination)
			if pErr != nil {
				return nil, pErr
			}
			req.ConflictPolicy = policy.ConflictPolicy
		}
	}

	// Steps 1–4: resolution pipeline (delegated, P0 #2). The helper
	// enforces RequireSubpath symmetrically across Publish and
	// ResolveFolder, eliminating the previous ResolveFolder bypass.
	resolved, err := p.resolveDestination(ctx, req)
	if err != nil {
		// PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE (July 2026): the
		// PathBuilder failed under ParentFolderID. The helper
		// returns BOTH the typed sentinel error AND the resolved
		// struct (with direct-to-root fallback). We preserve caller
		// backward-compat by errors.Is'ing the sentinel + log.Debug
		// ack + err=nil (godlike/07 minimum-blast-radius); the
		// struct's RootFolderID/PathSegments (with PathSegments=nil)
		// are still valid for the Step 6 PutFile seam. Aggressive-mode
		// callers can detect this case via the same errors.Is probe
		// and fail-closed (forward-pointer PR-VO-AGGREGATE-SUBPATH-CASCADE).
		if errors.Is(err, ErrPathBuilderIncompleteForParent) {
			p.log.Debug(
				"delivery: incomplete subpath tolerated because override was set",
				zap.String("destination", string(req.Destination)),
				zap.String("root_folder_id", resolved.RootFolderID),
				zap.Error(err),
			)
			err = nil
		} else {
			return nil, err
		}
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
		// PR-CLIPINGEST-PIPELINE step 9 (July 2026): thread the
		// canonical size + content-hash verification signals
		// (req.SizeBytes + req.ContentHash) into
		// PutFileRequest.ExpectedSize + PutFileRequest.ExpectedSHA256.
		// The uploader's verifyUploadedFile gate consumes these and
		// the post-upload UploadVerifier rejects mismatches per
		// the user-spec literal "Upload verificato per size+checksum
		// prima della cancellazione locale".
		ExpectedSize:   req.SizeBytes,
		ExpectedSHA256: req.ContentHash,
		PublicRead:     req.Destination == delivery.DestinationImage,
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
		// PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE (July 2026): companion
		// to Publish — see Publish for the full context. ResolveFolder
		// preserves caller backward-compat via the same errors.Is +
		// log.Debug ack + err=nil pattern. The resolved.FolderID is
		// still the override root (segments=nil → direct-to-root),
		// which is the canonical return value for callers that
		// ResolveFolder'd to learn the upload destination pre-publish.
		if errors.Is(err, ErrPathBuilderIncompleteForParent) {
			p.log.Debug(
				"delivery: incomplete subpath tolerated because override was set",
				zap.String("destination", string(req.Destination)),
				zap.String("root_folder_id", resolved.RootFolderID),
				zap.Error(err),
			)
			err = nil
		} else {
			return "", err
		}
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
