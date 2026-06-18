package storage

import (
	"context"
	"fmt"
	"path/filepath"

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
	if s == nil {
		return
	}
	s.treeService = svc
}

// SetTreeSource binds a Drive folder ID to a logical tree source name.
// Used by EnsureDriveFolder as a cache layer over the asset-tree service:
//
//	mediaStore.SetTreeSource(dests.VideoAIRoot, "videoai")
//	mediaStore.SetTreeSource(dests.ImagesFolder(), "image")
func (s *Store) SetTreeSource(folderID, source string) {
	if s == nil {
		return
	}
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
