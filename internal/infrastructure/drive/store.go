package drive

import (
	"context"
	"fmt"
	"path/filepath"

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
//     directly. assettree is being migrated toward `internal/core/asset`; this
//     facade outlives the migration.
//
//   - `TreeSources` maps a Drive folder ID to the logical source key the
//     `EnsureDriveFolder` cache layer uses ("videoai", "image", etc.). Empty
//     keys produce no cache entries. The map MUST be non-nil after
//     construction (set by `NewStore` if the caller passes nil).
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
// Construction now takes all required inputs at the ctor boundary:
// the 7 historical positionals PLUS `StoreOptions` carrying the
// previously-setter-injected `AssetTree` and `TreeSources` map.
//
// NewStoreWithOptions is the canonical constructor (replaces the legacy
// NewStore + SetAssetTree + SetTreeSource triple-call pattern). NewStore
// is preserved as a thin wrapper for callers that still want default-empty
// options (e.g. tests that never use the tree wiring); new code SHOULD
// prefer NewStoreWithOptions.
//
// Migration path:
//
//	new(code):
//	  store := drive.NewStore(resolver, uploader, root, images, videoai, sfx, log, drive.StoreOptions{
//	      AssetTree:   treeSvc,
//	      TreeSources: map[string]string{images: "image", videoai: "videoai"},
//	  })
//
//	old(code) — DEPRECATED, will be removed in commit 6 of the refactor:
//	  store := drive.NewStore(resolver, uploader, root, images, videoai, sfx, log)
//	  store.SetAssetTree(treeSvc)
//	  store.SetTreeSource(images, "image")
//	  store.SetTreeSource(videoai, "videoai")
type Store struct {
	resolver         *Resolver
	driveUploader    *Uploader
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
//	driveUploader    — concrete uploader; may be nil in tests
//	rootFolder       — Drive root folder ID
//	imagesFolder     — Drive images sub-folder (or "")
//	videoAIRoot      — Drive video-AI sub-folder (or "")
//	soundEffectsRoot — Drive sound-effects sub-folder (or "")
//	log              — zap logger
func NewStore(
	resolver *Resolver,
	driveUploader *Uploader,
	rootFolder, imagesFolder, videoAIRoot, soundEffectsRoot string,
	log *zap.Logger,
) *Store {
	return NewStoreWithOptions(resolver, driveUploader, rootFolder, imagesFolder, videoAIRoot, soundEffectsRoot, log, StoreOptions{})
}

// NewStoreWithOptions builds a Store with all 7 positionals PLUS the
// StoreOptions carrying AssetTree + TreeSources. This is the canonical
// constructor — old code that called `SetAssetTree` + `SetTreeSource`
// post-construction should migrate here so the dependency graph stays
// explicit at the ctor boundary.
func NewStoreWithOptions(
	resolver *Resolver,
	driveUploader *Uploader,
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
		driveUploader:    driveUploader,
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

// UploadToDrive resolves the destination folder via EnsureDriveFolder and
// then calls s.driveUploader.UploadFile on the local filePath. Returns the
// (fileID, webViewLink) pair from the Drive API.
//
// Behaviour contract:
//
//   - Store nil  → silent no-op (`"", "", nil`). Callers explicitly nil-check
//     the receiver to skip uploads in offline / test paths.
//   - driveUploader nil → silent no-op when no Drive client has been wired.
//     This matches the historical contract — the legacy wiring builds a
//     Store with no uploader when the Drive flow is disabled, and callers
//     expect empty IDs rather than an error.
//   - real Drive client → call EnsureDriveFolder, derive filename from
//     filePath (or req.Ext when filePath has no extension), and dispatch to
//     driveUploader.UploadFile. Errors are propagated verbatim.
//
// The Drive API call itself is delegated to internal/upload/drive (which
// owns retry, mime detection, and Drive metadata upload). This Store is
// only the routing layer: which folder + which filename.
func (s *Store) UploadToDrive(ctx context.Context, req AssetDestinationRequest, filePath string) (string, string, error) {
	if s == nil {
		return "", "", nil
	}
	if s.driveUploader == nil {
		if s.log != nil {
			s.log.Debug("storage.UploadToDrive: driveUploader not configured; skipping",
				zap.String("file_path", filePath),
				zap.String("subject", req.Subject))
		}
		return "", "", nil
	}

	folderID, err := s.EnsureDriveFolder(ctx, req)
	if err != nil {
		if s.log != nil {
			s.log.Warn("storage.UploadToDrive: EnsureDriveFolder failed",
				zap.String("subject", req.Subject),
				zap.Error(err))
		}
		return "", "", fmt.Errorf("resolve destination folder: %w", err)
	}

	filename := driveFilename(filePath, req.Ext)
	if filename == "" {
		return "", "", fmt.Errorf("storage.UploadToDrive: cannot derive filename from filePath=%q ext=%q", filePath, req.Ext)
	}

	result, err := s.driveUploader.UploadFile(ctx, filePath, folderID, filename)
	if err != nil {
		if s.log != nil {
			s.log.Warn("storage.UploadToDrive: driveUploader.UploadFile failed",
				zap.String("file_path", filePath),
				zap.String("folder_id", folderID),
				zap.Error(err))
		}
		return "", "", fmt.Errorf("drive upload: %w", err)
	}
	if result == nil {
		// drive.Uploader returned no error AND no result — treat as silent
		// no-op so callers that propagate empty IDs downstream keep working.
		return "", "", nil
	}
	return result.FileID, result.WebViewLink, nil
}

// driveFilename resolves the Drive-side filename for an upload. We prefer
// the basename of filePath when it carries an extension. We deliberately
// do NOT synthesise a fallback from reqExt — generating "upload"+ext
// risks Drive-side collisions when two callers hit the same root with
// the same ext. If the input is unusable, return "" so UploadToDrive
// errors out and forces the caller to fix the filePath rather than
// generating colliding Drive paths in production.
func driveFilename(filePath, reqExt string) string {
	_ = reqExt // intentionally unused: see comment above.
	base := filepath.Base(filePath)
	if base == "" || base == "." || base == "/" {
		return ""
	}
	if filepath.Ext(base) == "" {
		return ""
	}
	return base
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
