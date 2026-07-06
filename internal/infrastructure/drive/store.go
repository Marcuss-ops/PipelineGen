package drive

import (
	"context"

	"go.uber.org/zap"
)

// StoreOptions carries the configuration that previously required explicit
// post-construction setter calls (`SetAssetTree`, `SetTreeSource`). Promoting
// these inputs to constructor arguments is part of the ci/archcheck-hard-fail
// + refactor(composition): build_<bundle>.go split (problem #8).
//
// Trait summary:
//
//   - `AssetTree` is accepted as `any` to avoid importing the assettree package
//
// The canonical assettree lives at `internal/application/assets/assettree`; this
//
//	  facade outlives the migration.
//
//	- `TreeSources` maps a Drive folder ID to the logical source key the
//	  `EnsureDriveFolder` cache layer uses ("videoai", "image", etc.). Empty
//	  keys produce no cache entries. The map MUST be non-nil after
//	  construction (set by `NewStore` if the caller passes nil).
//
// Lifecycle: StoreOptions is consumed once at construction; mutate the Store
// by replacing the dependency, not by mutating the options struct in place.
type StoreOptions struct {
	// AssetTree is the *assettree.Service used to compute style folder paths
	// before falling back to Drive API lookups. nil-safe.
	AssetTree any

	// TreeSources maps Drive folder IDs to logical tree source names. Used
	// by EnsureDriveFolder as a cache layer over the asset-tree service.
	TreeSources map[string]string
}

// Store is the legacy wrapper sitting between the image/audio upload
// call sites and the underlying drive.Uploader. It bundles:
//
//   - a Resolver for path-only destination lookups (no Drive calls);
//   - the configured Drive folder tree (root + images + video-ai + sfx);
//   - an asset-tree service for style-aware folder resolution (optional);
//   - a zap logger.
//
// P0-2 (July 2026): UploadToDrive was REMOVED — all application-layer
// callers now route through delivery.Publisher.Publish. The remaining
// methods (EnsureDriveFolder, ResolveDest) are still used by the
// images package for folder resolution and path-only destination
// lookups via the tree-source-map cache and the root/ images/ videoAI
// / soundEffects folder IDs populated at construction.
//
// P0-2 slim (July 2026): driveUploader was REMOVED from the struct —
// it was dead weight post-UploadToDrive removal (no remaining method
// referenced it).
//
// Construction now takes all required inputs at the ctor boundary:
// the 7 historical positionals PLUS `StoreOptions` carrying the
// previously-setter-injected `AssetTree` and `TreeSources` map.
//
// NewStoreWithOptions is the canonical constructor (replaces the legacy
// NewStore + SetAssetTree + SetTreeSource triple-call pattern). NewStore
// is preserved as a thin wrapper for callers that still want default-empty
// options (e.g. tests that never use the tree wiring); new code SHOULD
// prefer NewStoreWithOptions.
type Store struct {
	resolver         *Resolver
	rootFolder       string
	imagesFolder     string
	videoAIRoot      string
	soundEffectsRoot string
	log              *zap.Logger

	treeService   any
	treeSourceMap map[string]string
}

// NewStore builds a Store with all 7 historical positionals and an empty
// StoreOptions. New code SHOULD prefer NewStoreWithOptions so the
// AssetTree/TreeSources inputs land at the ctor boundary instead of via
// post-construction setter calls.
//
// Arguments:
//
//	resolver         — path-only Resolver (for ResolveDest)
//	rootFolder       — Drive root folder ID
//	imagesFolder     — Drive images sub-folder (or "")
//	videoAIRoot      — Drive video-AI sub-folder (or "")
//	soundEffectsRoot — Drive sound-effects sub-folder (or "")
//	log              — zap logger
func NewStore(
	resolver *Resolver,
	rootFolder, imagesFolder, videoAIRoot, soundEffectsRoot string,
	log *zap.Logger,
) *Store {
	return NewStoreWithOptions(resolver, rootFolder, imagesFolder, videoAIRoot, soundEffectsRoot, log, StoreOptions{})
}

// NewStoreWithOptions builds a Store with all 7 positionals PLUS the
// StoreOptions carrying AssetTree + TreeSources. This is the canonical
// constructor — old code that called `SetAssetTree` + `SetTreeSource`
// post-construction should migrate here so the dependency graph stays
// explicit at the ctor boundary.
func NewStoreWithOptions(
	resolver *Resolver,
	rootFolder, imagesFolder, videoAIRoot, soundEffectsRoot string,
	log *zap.Logger,
	opts StoreOptions,
) *Store {
	src := opts.TreeSources
	if src == nil {
		src = map[string]string{}
	}
	return &Store{
		resolver:         resolver,
		rootFolder:       rootFolder,
		imagesFolder:     imagesFolder,
		videoAIRoot:      videoAIRoot,
		soundEffectsRoot: soundEffectsRoot,
		log:              log,
		treeService:      opts.AssetTree,
		treeSourceMap:    src,
	}
}

// EnsureDriveFolder returns the Drive folder ID where the request
// belongs. Selection order:
//
//  1. audio/sfx request    → soundEffectsRoot (if non-empty)
//  2. image/video request  → videoAIRoot or imagesFolder (style-aware)
//  3. fallthrough          → rootFolder
//
// Uses the TreeSources cache populated at construction (no post-ctor
// setter) to short-circuit the lookup when a folder ID is mapped.
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
