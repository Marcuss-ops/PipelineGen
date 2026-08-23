// Package drive — location_verifier.go implements the
// AssetLocationVerifier port with deep Drive + SQLite cross-reference.
//
// Unlike the lighter AssetLocationResolverAdapter (which only does
// Drive API checks), this verifier cross-references against SQLite
// to detect orphan files and broken asset locations. It also performs
// deeper file validation (MIME type, size > 0).
//
// Composition root wires this where AssetLocationVerifier is needed
// (deep repair CLI, asset audit tools).
package drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	domainasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// AssetStoreLookup is the narrow surface the LocationVerifier needs
// from the asset store. Declared locally so the drive package does
// not depend on the full asset.Service surface.
type AssetStoreLookup interface {
	// GetAsset returns the full asset details including locations.
	// Returns nil, nil when no asset with the given ID exists.
	GetAsset(ctx context.Context, assetID string) (*domainasset.Details, error)
}

// LocationVerifier implements script.AssetLocationVerifier with
// deep Drive API checks and SQLite cross-reference.
type LocationVerifier struct {
	reader Reader
	assets AssetStoreLookup
}

// NewLocationVerifier constructs the verifier from a Reader port
// and an asset store for SQLite cross-reference. The asset store
// may be nil — when nil, ORPHAN/BROKEN detection is skipped and
// the verifier falls back to the resolver's simpler states.
func NewLocationVerifier(reader Reader, assets AssetStoreLookup) *LocationVerifier {
	return &LocationVerifier{reader: reader, assets: assets}
}

// Verify checks whether the given Drive file is still accessible
// and cross-references against SQLite to detect orphan and broken
// states.
//
// When the asset store is nil, ORPHAN/BROKEN detection is skipped
// and the verifier behaves like the resolver adapter.
func (v *LocationVerifier) Verify(
	ctx context.Context,
	assetID string,
	fileID string,
	link string,
) (*scriptpkg.VerifiedLocation, error) {
	if strings.TrimSpace(fileID) == "" {
		return &scriptpkg.VerifiedLocation{
			AssetID:   assetID,
			State:     scriptpkg.LocationStateMalformed,
			ErrorCode: "EMPTY_FILE_ID",
		}, nil
	}
	if v.reader == nil {
		return nil, fmt.Errorf("drive: LocationVerifier: reader port is nil (composition gap)")
	}

	// Fetch file metadata from Drive.
	meta, err := v.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		return v.classifyDriveError(ctx, assetID, fileID, err)
	}
	if meta == nil {
		return v.classifyMissing(ctx, assetID, fileID, "NULL_META")
	}
	if errorCode := invalidDriveMetadataIdentity(meta, fileID); errorCode != "" {
		return v.classifyMissing(ctx, assetID, fileID, errorCode)
	}

	// File exists — check trashed state.
	if meta.Trashed {
		return &scriptpkg.VerifiedLocation{
			AssetID:     assetID,
			DriveFileID: fileID,
			State:       scriptpkg.LocationStateTrashed,
		}, nil
	}

	// File exists and is accessible — validate MIME type and size.
	if !isValidDriveFile(meta) {
		return v.classifyMissing(ctx, assetID, fileID,
			fmt.Sprintf("INVALID_FILE: mime=%s size=%d", meta.MimeType, meta.Size))
	}

	// Cross-reference: does this asset exist in SQLite?
	if v.assets != nil {
		if _, err := v.assets.GetAsset(ctx, assetID); err != nil {
			// Asset not found in SQLite — the file is an orphan.
			return &scriptpkg.VerifiedLocation{
				AssetID:     assetID,
				DriveFileID: fileID,
				DriveLink:   meta.WebViewLink,
				State:       scriptpkg.LocationStateOrphanDriveFile,
				ErrorCode:   "ASSET_NOT_IN_SQLITE",
			}, nil
		}
	}

	// Determine canonical link and state.
	canonicalLink := meta.WebViewLink
	if canonicalLink == "" {
		canonicalLink = FileURLFromID(fileID)
	}

	state := scriptpkg.LocationStateVerified
	if strings.TrimSpace(link) == "" ||
		!driveLinksEquivalent(link, canonicalLink, fileID) {
		state = scriptpkg.LocationStateUpdated
	}

	return &scriptpkg.VerifiedLocation{
		AssetID:     assetID,
		DriveFileID: fileID,
		DriveLink:   canonicalLink,
		State:       state,
	}, nil
}

// classifyDriveError maps a Drive API error to the appropriate
// state. For 404 (not found), cross-references with SQLite to
// distinguish MISSING from BROKEN_ASSET_LOCATION.
func (v *LocationVerifier) classifyDriveError(
	ctx context.Context,
	assetID, fileID string,
	err error,
) (*scriptpkg.VerifiedLocation, error) {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch gerr.Code {
		case http.StatusNotFound:
			return v.classifyMissing(ctx, assetID, fileID, "NOT_FOUND")
		case http.StatusForbidden:
			return &scriptpkg.VerifiedLocation{
				AssetID:     assetID,
				DriveFileID: fileID,
				State:       scriptpkg.LocationStateInaccessible,
				ErrorCode:   "PERMISSION_DENIED",
			}, nil
		}
	}
	return nil, fmt.Errorf("drive: failed to verify file %s: %w", fileID, err)
}

// classifyMissing determines whether a not-found file is simply
// MISSING or a BROKEN_ASSET_LOCATION (SQLite still references it).
func (v *LocationVerifier) classifyMissing(
	ctx context.Context,
	assetID, fileID, errorCode string,
) (*scriptpkg.VerifiedLocation, error) {
	// If no asset store, we can't distinguish MISSING from BROKEN.
	if v.assets == nil {
		return &scriptpkg.VerifiedLocation{
			AssetID:     assetID,
			DriveFileID: fileID,
			State:       scriptpkg.LocationStateMissing,
			ErrorCode:   errorCode,
		}, nil
	}

	// Check if the asset still exists in SQLite.
	details, err := v.assets.GetAsset(ctx, assetID)
	if err != nil {
		// Asset lookup failed — treat as MISSING (we can't
		// determine if it's broken without the DB).
		return &scriptpkg.VerifiedLocation{
			AssetID:     assetID,
			DriveFileID: fileID,
			State:       scriptpkg.LocationStateMissing,
			ErrorCode:   errorCode,
		}, nil
	}

	// Asset exists in SQLite but the file is gone from Drive.
	// Try to find an alternative canonical location.
	if details != nil {
		driveLoc := details.DriveLocation()
		if driveLoc != nil && driveLoc.ExternalID != "" &&
			driveLoc.ExternalID != fileID {
			// A different Drive location exists — the old
			// file_id is stale but there's a new one.
			newLink := driveLoc.AccessURL
			if newLink == "" {
				newLink = FileURLFromID(driveLoc.ExternalID)
			}
			return &scriptpkg.VerifiedLocation{
				AssetID:     assetID,
				DriveFileID: driveLoc.ExternalID,
				DriveLink:   newLink,
				State:       scriptpkg.LocationStateUpdated,
				ErrorCode:   errorCode + "_HAS_NEWER_LOCATION",
			}, nil
		}
	}

	// Asset exists in SQLite, file gone, no alternative location.
	return &scriptpkg.VerifiedLocation{
		AssetID:     assetID,
		DriveFileID: fileID,
		State:       scriptpkg.LocationStateBrokenAssetLocation,
		ErrorCode:   errorCode,
	}, nil
}

// isValidDriveFile returns true when the file metadata passes
// basic sanity checks (non-trivial MIME type, size > 0).
// Google Docs (application/vnd.google-apps.document) are
// accepted at size 0 since they have no physical bytes.
func isValidDriveFile(meta *FileMeta) bool {
	if meta == nil {
		return false
	}
	if meta.Size <= 0 && meta.MimeType != "application/vnd.google-apps.document" {
		return false
	}
	// MIME type should be non-empty for any valid Drive file.
	if strings.TrimSpace(meta.MimeType) == "" {
		return false
	}
	return true
}

// Compile-time pins: LocationVerifier satisfies both
// AssetLocationVerifier (the narrow Verify-only port) and
// AssetLocationResolver (the broader ResolveAndVerify port).
var (
	_ scriptpkg.AssetLocationVerifier = (*LocationVerifier)(nil)
	_ scriptpkg.AssetLocationResolver = (*LocationVerifier)(nil)
)

// ResolveAndVerify implements AssetLocationResolver. It extracts
// the file ID from the link (falling back to currentFileID) and
// delegates to Verify.
func (v *LocationVerifier) ResolveAndVerify(
	ctx context.Context,
	assetID, currentFileID, currentLink string,
) (*scriptpkg.VerifiedLocation, error) {
	if strings.TrimSpace(currentFileID) == "" && strings.TrimSpace(currentLink) == "" {
		return nil, nil
	}
	if v.reader == nil {
		return nil, fmt.Errorf("drive: LocationVerifier: reader port is nil (composition gap)")
	}

	fileID := strings.TrimSpace(currentFileID)
	if fileID == "" {
		fileID = FileIDFromLink(currentLink)
	}
	if fileID == "" {
		return &scriptpkg.VerifiedLocation{
			AssetID:   assetID,
			State:     scriptpkg.LocationStateMalformed,
			ErrorCode: "MALFORMED_LINK",
		}, nil
	}

	return v.Verify(ctx, assetID, fileID, currentLink)
}
