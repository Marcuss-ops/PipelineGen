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

import "errors"

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

// ErrPathBuilderIncompleteForOverride is the canonical sentinel returned
// when a Destination's PathBuilder fails (e.g., missing Group/Subject/Language
// metadata) AND the caller supplied a RootFolderOverride (the back-compat
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
var ErrPathBuilderIncompleteForOverride = errors.New("drive: PathBuilder incomplete but RootFolderOverride is set (direct-to-root fallback)")
