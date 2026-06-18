package storage

import (
	"path/filepath"
	"strings"
)

// Resolver resolves a logical destination request into local-only paths.
// It does NOT call Drive; that responsibility belongs to Store. The
// split exists because sound_effect_handlers.go uses Resolver directly
// (path only) while the image/video paths use Store.EnsureDriveFolder.
//
// Constructed with MediaRoot + DriveRoot. Both are kept on the struct so
// future implementations can validate the drive root is non-empty.
type Resolver struct {
	mediaRoot string
	driveRoot string
}

// NewResolver builds a path-only resolver rooted at (mediaRoot, driveRoot).
// Both arguments are accepted but not validated — empty roots fall back
// to local-relative paths at Resolve().
func NewResolver(mediaRoot, driveRoot string) *Resolver {
	return &Resolver{mediaRoot: mediaRoot, driveRoot: driveRoot}
}

// MediaRoot is a tiny passthrough helpers used directly by app wiring
// (compose_core.go, artlist.go): the historical pattern was
//
//	storageResolver := storage.NewResolver(
//	    storage.MediaRoot(cfg.Storage.MediaPath()),
//	    storage.DriveRoot(dests.RootFolder()),
//	)
//
// so MediaRoot + DriveRoot exist as the only path-normalisation hooks we
// need. Today they round-trip the value; tomorrow they can apply
// canonicalisation rules without changing call sites.
func MediaRoot(path string) string {
	return strings.TrimRight(path, "/")
}

// DriveRoot mirrors MediaRoot for the Drive folder ID column. Drive IDs
// are alphanumeric — no whitespace / path normalisation required, so the
// passthrough is correct.
func DriveRoot(folderID string) string {
	return folderID
}

// Resolve computes the local-only destination for a request.
//
// Algorithm (mirrors the original media/storage.Resolver.Resolve path-style):
//
//	rel  = <source>/<subject>.<ext>
//	root = <mediaRoot>/<rel>
//
// Style and Group are NOT encoded here; the asset-tree service (in Store)
// handles folder grouping when present. We deliberately keep this method
// pure-path so it can be safely called without a Drive client (used in
// tests that don't want to hit the Drive API).
func (r *Resolver) Resolve(req AssetDestinationRequest) (*ResolvedDest, error) {
	if r == nil {
		return &ResolvedDest{}, nil
	}
	// sanity defaults so a partially-populated request still resolves
	if req.Subject == "" {
		req.Subject = "unknown"
	}
	if req.Ext == "" {
		req.Ext = ".bin"
	}
	sourceSegment := string(req.Source)
	if sourceSegment == "" {
		sourceSegment = "media"
	}
	rel := filepath.Join(sourceSegment, req.Subject+req.Ext)
	root := r.mediaRoot
	if root == "" {
		return &ResolvedDest{RelativePath: rel}, nil
	}
	return &ResolvedDest{
		RelativePath: rel,
		LocalPath:    filepath.Join(root, rel),
	}, nil
}
