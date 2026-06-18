package storage

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
)

// Store is the legacy wrapper sitting between the image/audio upload
// call sites and the underlying drive.Uploader. It bundles:
//
//   - a Resolver for path-only destination lookups (no Drive calls);
//   - the configured Drive folder tree (root + images + video-ai + sfx);
//   - an asset-tree service for style-aware folder resolution;
//   - a zap logger.
//
// Construction signature mirrors the historical call pattern in
// compose_core.go / artlist.go:
//
//	mediaStore := storage.NewStore(
//	    storageResolver,
//	    driveUploader,
//	    dests.RootFolder(),
//	    dests.ImagesFolder(),
//	    dests.VideoAIRoot,
//	    dests.SoundEffectsRoot,
//	    log,
//	)
//
// All 7 positionals are required and order-stable across Phase-7 callers.
type Store struct {
	resolver         *Resolver
	driveUploader    *drive.Uploader
	rootFolder       string
	imagesFolder     string
	videoAIRoot      string
	soundEffectsRoot string
	log              *zap.Logger

	treeService   any // *media.assettree.Service — accepted as `any` to break the import cycle
	treeSourceMap map[string]string
}

// NewStore builds a Store with all 7 historical positionals.
//
// Arguments:
//
//	resolver         — path-only Resolver (for ResolveDest)
//	driveUploader    — concrete uploader; may be nil in tests
//	rootFolder       — Drive root folder ID
//	imagesFolder     — Drive images sub-folder (or "")
//	videoAIRoot      — Drive video-AI sub-folder (or "")
//	soundEffectsRoot — Drive sound-effects sub-folder (or "")
//	log              — zap logger
func NewStore(
	resolver *Resolver,
	driveUploader *drive.Uploader,
	rootFolder, imagesFolder, videoAIRoot, soundEffectsRoot string,
	log *zap.Logger,
) *Store {
	return &Store{
		resolver:         resolver,
		driveUploader:    driveUploader,
		rootFolder:       rootFolder,
		imagesFolder:     imagesFolder,
		videoAIRoot:      videoAIRoot,
		soundEffectsRoot: soundEffectsRoot,
		log:              log,
		treeSourceMap:    make(map[string]string),
	}
}

// SetAssetTree installs the asset-tree service used to compute style
// folder paths before falling back to Drive API lookups.
//
// The legacy type was a *media.assettree.Service. We accept any here to
// avoid importing the assettree package directly — assettree is being
// moved towards internal/core/asset, and this facade should outlive that
// migration.
func (s *Store) SetAssetTree(svc any) {
	s.treeService = svc
}

// SetTreeSource binds a Drive folder ID to a logical tree source name.
// Used by EnsureDriveFolder as a cache layer over the asset-tree service:
//
//	mediaStore.SetTreeSource(dests.VideoAIRoot, "videoai")
//	mediaStore.SetTreeSource(dests.ImagesFolder(), "image")
func (s *Store) SetTreeSource(folderID, source string) {
	if s.treeSourceMap == nil {
		s.treeSourceMap = make(map[string]string)
	}
	s.treeSourceMap[folderID] = source
}

// EnsureDriveFolder returns the Drive folder ID where the request
// belongs. Selection order:
//
//  1. audio/sfx request    → soundEffectsRoot (if non-empty)
//  2. image/video request  → videoAIRoot or imagesFolder (style-aware)
//  3. fallthrough          → rootFolder
//
// STUB: a real implementation should consult the asset-tree cache
// before reaching the Drive API. This facade-only version keeps the
// build green while the migration lands.
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

// UploadToDrive is the legacy 3-arg signature used by all image/audio/
// video upload paths. STUB: a real implementation must call
// s.driveUploader.UploadFile / UploadToDrive with the resolved folder.
// This version returns empty IDs so the build passes; a follow-up PR
// will wire the actual Drive upload.
func (s *Store) UploadToDrive(_ context.Context, _ AssetDestinationRequest, _ string) (string, string, error) {
	if s == nil {
		return "", "", nil
	}
	return "", "", nil
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
