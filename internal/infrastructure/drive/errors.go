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

// ErrLegacySurfaceRetired is the infrastructure-layer typed sentinel
// surfaced by the legacy Drive-upload surface fail-closed stubs after
// P2.2 DRIVE-008 closure (commit `0fa8c065`).
//
// godlike/06 §"one owner per fact" + godlike/07 typed-error contract:
// the canonical migration destination is `delivery.Publisher.Publish`.
// Consumers probe the seam via `errors.Is(err, drive.ErrLegacySurfaceRetired)`
// without crossing the application/infrastructure layering boundary.
// The application-layer counterpart at
// `internal/application/clips/ports.go::clips.ErrLegacySurfaceRetired`
// is wrapped alongside this sentinel via Go 1.20+ multi-%w in the
// production fail-closed stub (`internal/app/clips_adapters_drive.go`)
// so either probe resolves cleanly. P0.8 inadvertently dropped this
// declaration during the ErrAmbiguousDriveFolder extraction; P2.5
// closure restored it because both the test surface (this package's
// `errors_test.go`) AND the production fail-closed stub call sites
// depend on the symbol being present.
//
// godlike/07 honest-limitation: the message string is byte-stable —
// `internal/infrastructure/drive/errors_test.go::TestErrLegacySurfaceRetired_Exists`
// pins the literal via the `want` constant, and any drift in this
// message string breaks the test layer (forward detection) before
// any wire consumer observes the drift.
var ErrLegacySurfaceRetired = errors.New("legacy drive upload surface retired: use delivery.Publisher.Publish (DRIVE-008)")
