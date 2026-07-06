package drive

import (
	"context"
)

// Store is the legacy wrapper sitting between the image/audio upload
// call sites and the configured Drive folder tree. It bundles:
//
//   - a Resolver for path-only destination lookups (no Drive calls);
//   - the configured Drive folder tree (root + images + video-ai + sfx).
//
// P0-2 (July 2026): UploadToDrive was REMOVED — all application-layer
// callers now route through delivery.Publisher.Publish. The remaining
// methods (EnsureDriveFolder, ResolveDest) are still used by the
// images package for folder resolution and path-only destination
// lookups.
//
// P0-2 slim #1 (July 2026): driveUploader was REMOVED — dead weight
// post-UploadToDrive removal.
//
// P0-2 slim #2 (July 2026): log, treeService, treeSourceMap were
// REMOVED — all three were assigned at construction but never read
// by any Store method. StoreOptions and NewStoreWithOptions were
// removed along with them; NewStore is the single canonical
// constructor.
type Store struct {
	resolver         *Resolver
	rootFolder       string
	imagesFolder     string
	videoAIRoot      string
	soundEffectsRoot string
}

// NewStore builds a Store with the folder tree and resolver.
//
// Arguments:
//
//	resolver         — path-only Resolver (for ResolveDest)
//	rootFolder       — Drive root folder ID
//	imagesFolder     — Drive images sub-folder (or "")
//	videoAIRoot      — Drive video-AI sub-folder (or "")
//	soundEffectsRoot — Drive sound-effects sub-folder (or "")
func NewStore(
	resolver *Resolver,
	rootFolder, imagesFolder, videoAIRoot, soundEffectsRoot string,
) *Store {
	return &Store{
		resolver:         resolver,
		rootFolder:       rootFolder,
		imagesFolder:     imagesFolder,
		videoAIRoot:      videoAIRoot,
		soundEffectsRoot: soundEffectsRoot,
	}
}

// EnsureDriveFolder returns the Drive folder ID where the request
// belongs. Selection order:
//
//  1. audio/sfx request    → soundEffectsRoot (if non-empty)
//  2. image/video request  → videoAIRoot or imagesFolder
//  3. fallthrough          → rootFolder
func (s *Store) EnsureDriveFolder(_ context.Context, req AssetDestinationRequest) (string, error) {
	if s == nil {
		return "", nil
	}
	switch {
	case req.MediaType == MediaTypeSoundEffect && s.soundEffectsRoot != "":
		return s.soundEffectsRoot, nil
	case req.MediaType == MediaTypeImageVideo && s.videoAIRoot != "":
		return s.videoAIRoot, nil
	case req.MediaType == MediaTypeImage && s.imagesFolder != "":
		return s.imagesFolder, nil
	}
	return s.rootFolder, nil
}

// ResolveDest is the path-only side of Resolve — it MUST NOT call Drive.
// Falls through to Resolver.Resolve when wired; returns empty struct
// when no resolver is configured (callers must handle that — they already
// do, via nil-safe derefs like `dest.LocalPath`).
func (s *Store) ResolveDest(req AssetDestinationRequest) (*ResolvedDest, error) {
	if s == nil {
		return &ResolvedDest{}, nil
	}
	if s.resolver != nil {
		return s.resolver.Resolve(req)
	}
	return &ResolvedDest{}, nil
}
