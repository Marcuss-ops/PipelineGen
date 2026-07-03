// Package drive — errors.go
//
// Canonical typed-error surface for the drive package.
//
// DRIVE-008 (July 2026): ErrLegacySurfaceRetired is the fail-closed
// sentinel for the legacy drive upload seam (UploadFile /
// UploadFileWithDescription) on the composition-root adapters
// (clipsDriveAdapter + sourcingDriveAdapter). The canonical upload
// path is delivery.Publisher.Publish (internal/application/assets/
// delivery/publisher.go) — any caller that still reaches the bypass
// surface via clips.ClipDriveUploaderPort.UploadFile or
// sourcing.DrivePort.UploadFileWithDescription receives this typed
// error immediately at runtime (errors.Is compatible). Per
// godlike/07 §"No fake availability", the legacy surface is loud at
// runtime rather than silently translating to a 200 OK.
//
// The sentinel lives in the drive package because the retirements
// map to the drive package's UploadFileWithDescription upstream
// (the bypass source that composition-root adapters were calling
// directly). The canonical replacement (delivery.Publisher.Publish)
// is the SINGLE AUTHORITATIVE upload surface per architecture/
// deprecations.yaml#DRIVE-008.
package drive

import "errors"

// ErrLegacySurfaceRetired is returned by the legacy drive upload
// seam (clips.ClipDriveUploaderPort.UploadFile + UploadFileWithDescription,
// sourcing.DrivePort.UploadFileWithDescription) when invoked.
// Production callers MUST migrate to delivery.Publisher.Publish —
// see architecture/deprecations.yaml#DRIVE-008 for the migration
// contract, replacement path, and compatibility test surface.
//
// Detect via errors.Is(err, drive.ErrLegacySurfaceRetired) at the
// caller / handler / API boundary. The error is wrapped one or two
// levels by adapter-specific context messages so callers retain
// adapter-level diagnostics (which surface + which arguments
// triggered the fail-closed).
var ErrLegacySurfaceRetired = errors.New("legacy drive upload surface retired: use delivery.Publisher.Publish (DRIVE-008)")
