// Package manifest — DriveAdapter port (PR 6 / PR 7 cutover).
//
// The manifest package does NOT import google.golang.org/api/drive/v3.
// All Drive interactions go through this narrow port; the production
// adapter is constructed in internal/app/manifest_adapters.go and wraps
// *drive.Uploader. Tests can swap a fake implementation.
package manifest

import "context"

// DriveAdapter is the narrow Drive-side port used by manifest.Service.
//
// Two methods only:
//
//   - DownloadManifest:  reads the current canonical `metadata.json`
//     from <folderID> in Drive. Returns
//     (nil, nil) when the file does not exist.
//     Bytes are returned verbatim; manifest
//     unmarshals them in memory.
//
//   - ReplaceManifest:   uploads <content> as `metadata.json` in
//     <folderID>. Honors the "upload-then-replace"
//     spec directive: if a non-trashed file with
//     the same name already exists, the adapter
//     MUST call google drive Files.Update on the
//     existing file id (NOT trash-then-create).
//     Returns the (possibly-new) file id.
//
// Keeping read + write as separate methods lets the manifest service
// own the merge-by-AssetID logic in one place while the adapter owns
// the SDK-level file-id bookkeeping.
type DriveAdapter interface {
	DownloadManifest(ctx context.Context, folderID, filename string) ([]byte, error)
	ReplaceManifest(ctx context.Context, folderID, filename string, content []byte) (fileID string, err error)
}
