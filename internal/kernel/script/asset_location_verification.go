// Package script — asset_location_verification.go defines the canonical
// contract for resolving and verifying asset locations against Google Drive.
//
// The resolver is the single source of truth for determining whether a
// drive_link in a SpecScene binding is still valid. It must be used
// before:
//   - Google Doc rendering
//   - SQLite persistence
//   - Manifest emission
//   - API response construction
//
// No drive_link may be written into a Google Doc without first passing
// through this resolver.
package script

import (
	"context"
	"time"
)

// LocationState encodes the canonical verification outcome for an
// asset's Drive location. Every drive_link consumed by a binding
// MUST carry one of these states after verification.
type LocationState string

const (
	// LocationStateVerified — file exists, not trashed, accessible.
	// The current drive_link is canonical and requires no change.
	LocationStateVerified LocationState = "VERIFIED"

	// LocationStateUpdated — file exists but the stored drive_link was
	// stale or malformed. The binding has been updated with the
	// canonical webViewLink from the current FileMeta.
	LocationStateUpdated LocationState = "UPDATED"

	// LocationStateMissing — Drive API returned not found for the
	// stored file ID. The file no longer exists.
	LocationStateMissing LocationState = "MISSING"

	// LocationStateTrashed — the file exists but trashed=true.
	LocationStateTrashed LocationState = "TRASHED"

	// LocationStateInaccessible — Drive API returned permission denied.
	// The file may exist but the service account cannot access it.
	LocationStateInaccessible LocationState = "INACCESSIBLE"

	// LocationStateMalformed — the stored drive_link could not be
	// parsed into a valid Drive file ID.
	LocationStateMalformed LocationState = "MALFORMED"

	// LocationStateOrphanDriveFile — the file exists on Drive but
	// has no corresponding asset/location record in SQLite. Do not
	// import automatically without sufficient metadata.
	LocationStateOrphanDriveFile LocationState = "ORPHAN_DRIVE_FILE"

	// LocationStateBrokenAssetLocation — SQLite has an ACTIVE asset
	// with a drive_file_id that no longer exists on Drive.
	LocationStateBrokenAssetLocation LocationState = "BROKEN_ASSET_LOCATION"

	// LocationStateDuplicate — two or more asset_id values share the
	// same drive_file_id, or a single asset has multiple ACTIVE
	// locations. The system must have exactly one canonical active
	// location unless explicit replica support is present.
	LocationStateDuplicate LocationState = "DUPLICATE"
)

// VerifiedLocation is the canonical result of resolving and verifying
// a single asset location. It carries the resolved state, the
// canonical drive_file_id and drive_link, and diagnostic metadata.
type VerifiedLocation struct {
	// AssetID is the canonical media_assets.id this location belongs to.
	AssetID string `json:"asset_id"`

	// DriveFileID is the canonical Google Drive file ID.
	// For VERIFIED/UPDATED this is the confirmed file ID.
	// For MISSING/MALFORMED this is the original (now invalid) file ID.
	DriveFileID string `json:"drive_file_id,omitempty"`

	// DriveLink is the canonical Google Drive webViewLink.
	// For UPDATED this is the freshly-resolved link.
	// For MISSING/TRASHED/INACCESSIBLE/MALFORMED this is empty.
	DriveLink string `json:"drive_link,omitempty"`

	// State is the canonical verification outcome.
	State LocationState `json:"state"`

	// ErrorCode preserves the underlying Drive or transport error
	// for diagnostics. Empty when State is VERIFIED or UPDATED.
	ErrorCode string `json:"error_code,omitempty"`

	// VerifiedAt is the wall-clock time of verification.
	VerifiedAt time.Time `json:"verified_at"`
}

// AssetLocationVerifier is the narrow port for verifying a single
// asset location. Unlike AssetLocationResolver (which resolves AND
// verifies), this interface only performs the verification step —
// callers that already have a resolved file ID can use this lighter
// contract without the resolution logic.
//
// The application layer owns this interface; the concrete
// implementation is the same *drive.AssetLocationResolverAdapter
// that satisfies AssetLocationResolver (the Verify method delegates
// to the same underlying Reader.GetFileMeta call).
type AssetLocationVerifier interface {
	// Verify checks whether the given Drive file is still accessible.
	//
	// Parameters:
	//   - assetID: the canonical media_assets.id
	//   - fileID:  the Google Drive file ID to verify
	//   - link:    the current drive_link string (used for comparison
	//              to detect UPDATED vs VERIFIED)
	//
	// Returns a VerifiedLocation on success. A non-nil error is
	// reserved for transport-level failures.
	Verify(ctx context.Context, assetID, fileID, link string) (*VerifiedLocation, error)
}

// AssetLocationResolver is the canonical port for resolving and
// verifying an asset's Drive location. Every drive_link that enters
// a SpecScene binding or Google Doc MUST pass through this resolver.
//
// The application layer owns this interface; the concrete
// implementation lives in internal/platform/drive and uses the
// Reader port (FileIsNotTrashed + GetFileMeta) to verify against the
// actual Drive API.
type AssetLocationResolver interface {
	// ResolveAndVerify checks whether the given asset's Drive
	// location is still valid.
	//
	// Parameters:
	//   - assetID:  the canonical media_assets.id
	//   - currentFileID: the drive_file_id currently stored for this
	//     asset (may be empty if only a link is known)
	//   - currentLink: the drive_link string from the binding
	//
	// When currentFileID is empty, the resolver extracts it from
	// currentLink via FileIDFromLink. When currentLink is also
	// empty, the resolver returns (nil, nil) — callers treat "no
	// link to verify" as a no-op.
	//
	// Returns a VerifiedLocation on success; the caller uses
	// State to decide whether to keep, update, or clear the link.
	// A non-nil error is reserved for transport-level failures
	// (network timeout, Drive API 5xx) — the caller MUST fail
	// closed in that case.
	ResolveAndVerify(ctx context.Context, assetID, currentFileID, currentLink string) (*VerifiedLocation, error)
}
