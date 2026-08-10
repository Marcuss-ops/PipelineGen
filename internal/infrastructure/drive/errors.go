// Package drive — errors.go (P0.8, July 2026)
//
// Canonical typed sentinels for Drive operations. Callers use errors.Is
// to distinguish ambiguity from transient failures without string-matching
// on error messages. Mirrors the per-package sentinel convention already
// established in publisher.go (ErrMissingDestinationRegistry,
// ErrMissingFolderManager, ErrMissingFileUploader) and uploader.go
// (ErrAmbiguousDriveFile).
//
// P0.8 (July 2026): ErrAmbiguousDriveFolder was extracted from
// folder_manager.go (where it was initially defined in P0.7) into this
// canonical errors home so it can be imported without pulling in the
// folder-manager dependency surface.
package drive

import (
	"errors"
	"net/http"

	"google.golang.org/api/googleapi"
)

// ErrAmbiguousDriveFolder is the canonical sentinel returned when
// a Drive Files.List query finds more than one non-trashed folder
// with the same name under the same parent. This is the folder-level
// parallel to ErrAmbiguousDriveFile for files.
//
// P0.7 (July 2026): the pre-fix findOrCreateFolder created a folder
// and returned created.Id without checking whether a cross-process
// race produced a duplicate. The re-lookup after Create now detects
// the >1 case and surfaces this sentinel so callers can fail-closed
// rather than silently returning a folder ID that may collide with
// another instance's folder.
//
// P0.8 (July 2026): firstFolderID was upgraded from silently returning
// the first match to returning this sentinel on >1 match. Callers
// (newDefaultFolderLookup, newAdminDefaultLookup) propagate the error
// through the retry seam, and callers errors.Is against this sentinel
// to distinguish ambiguity from transient failures.
var ErrAmbiguousDriveFolder = errors.New("drive: ambiguous folder match: multiple non-trashed folders with the same name+parent exist on Drive")

// ErrDriveListNil is the canonical sentinel returned when a Drive
// Files.List call returns (nil, nil) — a known edge case in the
// google-api-go-client where context cancellation or partial response
// produces a nil list without an error. Without this sentinel the
// next nil-deref on `list.Files` triggers HTTP 500 via panic-recovery
// in the handler middleware (panic stack frame
// "internal/infrastructure/drive.(*Uploader).SearchFiles" fired during
// POST /api/media/register-batch — the wire-shape only flow that
// reaches SearchFiles through the async clip-enqueue adapter).
//
// godlike/07 no-fake-availability: callers errors.Is against this
// sentinel so the empty-result (genuinely-empty Drive query) and the
// nil-list (client edge case) are distinct from transient errors
// (HTTP 500 → retryable via pkg/retry.IsTransient) and from real
// failures (HTTP 500 → actionable).
//
// Forward-pointer: PR-DRIVE-LIST-NIL-GUARD-AUDIT (deadline 2026-08-01)
// audits the other internal/infrastructure/drive/*.go list-response
// dereferences for the same nil-list edge case.
var ErrDriveListNil = errors.New("drive: Files.List returned nil result with nil error (known client edge case; callers must errors.Is to distinguish from empty)")

// ErrLegacySurfaceRetired was retired in DRIVE-008 CUTOVER (June 2026,
// commit 0fa8c065). The sentinel is preserved as a comment-only
// historical audit-pin (no live var) so future agents can trace
// the DRIVE-008 fail-closed stub lineage from the codebase.
// FASE 0.3 (July 2026): the 3 fail-closed stubs (UploadFile +
// UploadFileWithDescription + sourcing.DrivePort.UploadFileWithDescription)
// retired via PR-YT-DRIVE-LEGACY-RETIRE.

// ErrPathBuilderIncompleteForParent is the canonical sentinel returned
// when a Destination's PathBuilder fails (e.g., missing Group/Subject/Language
// metadata) AND the caller supplied a ParentFolderID (the back-compat
// escape hatch for pre-subpath-era callers).
//
// godlike/07 typed-error contract (PR-VO-ERR-PATHBUILDER-INCOMPLETE-OVERRIDE,
// July 2026; replaces the original log.Warn + silent-swallow emission in
// Publisher.resolveDestination Step 4): callers can errors.Is to detect
// "incomplete subpath tolerated because override was set". The sentinel is
// wrapped via fmt.Errorf dual-%w (Go 1.20+) per godlike/07 dual-wrapping
// discipline — the underlying PathBuilder cause is preserved via the first
// %w for errors.As recovery while the typed sentinel is exposed via the
// second %w for errors.Is probes. Single-line message avoids the
// newline-separated stderr noise of errors.Join, preserving
// grep-ability for log aggregators.
//
// Decision: this is a SEMANTIC CHANGE from prior behaviour (silent swallow
// with log.Warn) to explicit typed-error (caller-decision via errors.Is +
// log.Debug ack). The pre-existing Publish/ResolveFolder call sites still
// opt-in to swallow the sentinel (preserving caller backward-compat per
// godlike/07 minimum-blast-radius). Aggressive-mode callers (future wiring
// of pre-publish integral checks, see forward-pointer
// PR-VO-AGGREGATE-SUBPATH-CASCADE) can errors.Is + return an error to
// fail-closed at fallback.
//
// Production timestamps: PR-VO-SUBFOLDER closure (commit 556bf906,
// 2026-07-04) shipped the originally-swallowed fallback. This sentinel
// migration is the typed-error contract that user-spec asked for in the
// PR-VO-SUBFOLDER follow-up.
var ErrPathBuilderIncompleteForParent = errors.New("drive: PathBuilder incomplete but ParentFolderID is set (direct-to-root fallback)")

// DriveIsNotFound is the canonical typed classifier for Google Drive
// HTTP 404 (file/folder not found) responses. The probe is a pure
// function (no sentinel): callers use the boolean return directly
// rather than probing with errors.Is.
//
// godlike/07 typed-error contract (per AGENTS.md §godlike/07 + the
// canonical googleapi.Error probe pattern documented in
// internal/application/jobs/outbox/voiceover_cleanup.go::isDriveNotFoundError):
// (a) errors.As walks the wrap chain so callers need NOT manually
// unwrap fmt.Errorf %w — the helper handles any wrap depth;
// (b) non-*googleapi.Error (plain errors.New, custom typed errors,
// nil) returns false — the predicate is strictly typed, never a
// substring-match fallback (FASE 6 Cut 6.1.D closed the legacy
// strings.Contains anti-pattern);
// (c) nil error returns false (nil-tolerance per godlike/07 so
// callers don't have to nil-guard before invoking).
//
// Production callers: FileIsNotTrashed + FileExists
// (internal/infrastructure/drive/uploader_file.go) delegate here;
// Other call-sites (verifier_adapter.go, document_builder.go,
// reconcile.go) consume the (fileID) (bool, error) return shapes
// unchanged (godlike/07 minimum-blast-radius — port signatures
// preserved across the closure).
func DriveIsNotFound(err error) bool {
	if err == nil {
		return false
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	return gerr.Code == http.StatusNotFound
}

// ErrDriveUploaderNotConfigured is the canonical sentinel returned by
// Store.UploadToDrive when the legacy Store was constructed without a
// driveUploader (nil *Uploader).
//
// P0-2 (July 2026, godlike/07 no-fake-availability): the pre-P0-2
// path silently returned ("", "", nil) when driveUploader was nil —
// callers that received empty IDs from a nil uploader skipped
// downstream Drive-side indexing/audit without any signal. The new
// contract returns this typed sentinel so callers can errors.Is to
// distinguish "uploader not configured" from "upload succeeded with
// empty IDs" (the former is a configuration gap, the latter is a
// Drive API edge case).
//
// Production sites that hit this sentinel should be audited for
// Store nil-receiver guards BEFORE the UploadToDrive call (the
// fail-closed path protects from silent-skip but a nil Store
// guard is the cleaner design — it surfaces the gap at
// composition time rather than at first upload).
var ErrDriveUploaderNotConfigured = errors.New("drive: uploader not configured (nil *Uploader passed to Store ctor — P0-2: application-layer callers route through delivery.Publisher.Publish, not Store.UploadToDrive)")
