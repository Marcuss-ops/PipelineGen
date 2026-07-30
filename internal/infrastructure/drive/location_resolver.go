// Package drive — location_resolver.go implements the canonical
// AssetLocationResolver port against the Google Drive API.
//
// The adapter uses the Reader port (FileIsNotTrashed + GetFileMeta)
// to verify file existence, trashed state, and accessibility, and
// returns the canonical drive_link from the current FileMeta.
//
// Composition root wires this where AssetLocationResolver is needed
// (processor_asset_location_reconciliation, repair CLI, etc.).
package drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// AssetLocationResolverAdapter implements script.AssetLocationResolver
// by verifying Drive files through the Reader port.
type AssetLocationResolverAdapter struct {
	reader Reader
}

// NewAssetLocationResolverAdapter constructs the adapter from a
// configured Reader port. Composition root wires this where the
// canonical AssetLocationResolver is needed.
func NewAssetLocationResolverAdapter(reader Reader) *AssetLocationResolverAdapter {
	return &AssetLocationResolverAdapter{reader: reader}
}

// ResolveAndVerify checks whether the given asset's Drive location
// is still valid. It extracts the file ID from currentLink (falling
// back to currentFileID), calls GetFileMeta to retrieve the current
// canonical state, and returns a VerifiedLocation with the
// appropriate LocationState.
//
// When both currentFileID and currentLink are empty, the call is a
// no-op (nil, nil). When the reader is nil, every call returns
// (nil, fmt.Errorf(...)) — callers MUST fail closed.
func (a *AssetLocationResolverAdapter) ResolveAndVerify(
	ctx context.Context,
	assetID string,
	currentFileID string,
	currentLink string,
) (*scriptpkg.VerifiedLocation, error) {
	// No-op: nothing to verify.
	if strings.TrimSpace(currentFileID) == "" && strings.TrimSpace(currentLink) == "" {
		return nil, nil
	}
	if a.reader == nil {
		return nil, fmt.Errorf("drive: AssetLocationResolverAdapter: reader port is nil (composition gap)")
	}

	// Determine the file ID to verify.
	fileID := strings.TrimSpace(currentFileID)
	if fileID == "" {
		fileID = FileIDFromLink(currentLink)
	}
	if fileID == "" {
		return &scriptpkg.VerifiedLocation{
			AssetID:     assetID,
			DriveFileID: currentFileID,
			DriveLink:   "",
			State:       scriptpkg.LocationStateMalformed,
			ErrorCode:   "MALFORMED_LINK",
		}, nil
	}

	// Fetch file metadata from Drive.
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		return a.classifyError(assetID, fileID, err)
	}
	if meta == nil {
		return &scriptpkg.VerifiedLocation{
			AssetID:     assetID,
			DriveFileID: fileID,
			State:       scriptpkg.LocationStateMissing,
			ErrorCode:   "NULL_META",
		}, nil
	}

	// File exists — check if it's in the trash.
	if meta.Trashed {
		return &scriptpkg.VerifiedLocation{
			AssetID:     assetID,
			DriveFileID: fileID,
			State:       scriptpkg.LocationStateTrashed,
		}, nil
	}

	// File exists and is accessible. Determine whether the link
	// needs updating.
	canonicalLink := meta.WebViewLink
	if canonicalLink == "" {
		canonicalLink = FileURLFromID(fileID)
	}

	state := scriptpkg.LocationStateVerified
	// Compare against the current link. If the current link is
	// empty or differs from the canonical one, flag as UPDATED.
	if strings.TrimSpace(currentLink) == "" ||
		!driveLinksEquivalent(currentLink, canonicalLink, fileID) {
		state = scriptpkg.LocationStateUpdated
	}

	return &scriptpkg.VerifiedLocation{
		AssetID:     assetID,
		DriveFileID: fileID,
		DriveLink:   canonicalLink,
		State:       state,
	}, nil
}

// classifyError maps a Drive API error to the appropriate
// LocationState. Permission denied (HTTP 403) → INACCESSIBLE;
// not found (HTTP 404) → MISSING; everything else surfaces as
// a transport error the caller must fail closed on.
func (a *AssetLocationResolverAdapter) classifyError(assetID, fileID string, err error) (*scriptpkg.VerifiedLocation, error) {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch gerr.Code {
		case http.StatusNotFound:
			return &scriptpkg.VerifiedLocation{
				AssetID:     assetID,
				DriveFileID: fileID,
				State:       scriptpkg.LocationStateMissing,
				ErrorCode:   "NOT_FOUND",
			}, nil
		case http.StatusForbidden:
			return &scriptpkg.VerifiedLocation{
				AssetID:     assetID,
				DriveFileID: fileID,
				State:       scriptpkg.LocationStateInaccessible,
				ErrorCode:   "PERMISSION_DENIED",
			}, nil
		}
	}
	// Unknown/transport error — surface to caller for fail-closed handling.
	return nil, fmt.Errorf("drive: failed to verify file %s: %w", fileID, err)
}

// driveLinksEquivalent returns true when two Drive links refer to
// the same file. It compares the extracted file IDs.
func driveLinksEquivalent(a, b, fileID string) bool {
	aID := FileIDFromLink(a)
	if aID == "" {
		return false
	}
	bID := FileIDFromLink(b)
	if bID == "" {
		return aID == fileID
	}
	return aID == bID
}

// Compile-time pin: AssetLocationResolverAdapter satisfies
// script.AssetLocationResolver.
var _ scriptpkg.AssetLocationResolver = (*AssetLocationResolverAdapter)(nil)
