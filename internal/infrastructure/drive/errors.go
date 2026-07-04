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

// ErrLegacySurfaceRetired was retired in DRIVE-008 CUTOVER (June 2026,
// commit 0fa8c065). The sentinel is preserved as a comment-only
// historical audit-pin (no live var) so future agents can trace
// the DRIVE-008 fail-closed stub lineage from the codebase.
// FASE 0.3 (July 2026): the 3 fail-closed stubs (UploadFile +
// UploadFileWithDescription + sourcing.DrivePort.UploadFileWithDescription)
// retired via PR-YT-DRIVE-LEGACY-RETIRE.
