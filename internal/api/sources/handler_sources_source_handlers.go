// Package sources owns the legacy flat HTTP handlers for media sources that
// have NOT yet been migrated to api/assets/.
//
// PR4 (June 2026): voiceover, soundeffect, and register-from-youtube
// extracted to api/assets/{voiceover,soundeffect,register}/.
package sources

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	clipsources "github.com/Marcuss-ops/PipelineGen/internal/api/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/catalog"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
)

// SourcesHandler handles non-clip media operations and routes the clip
// surface via the embedded clipsources.Handler.
//
// PR4 (June 2026): Voiceover, SoundEffect, and RegisterFromYouTube moved
// to api/assets/. SyncDriveFolder and LocalToDrive remain as thin wrappers.
type SourcesHandler struct {
	cfg            *config.Config
	jobsSvc        *jobservice.Service
	catalogRepo    *catalog.Repository
	assetIndexSvc  *assetindex.Service
	artlistRepo    *assets.ClipsRepository
	clipsRepo      *assets.ClipsRepository
	stockRepo      *assets.ClipsRepository
	voiceoverRepo  *assets.VoiceoversRepository
	imagesRepo     *assets.ImagesRepository
	folderMemSvc   *foldermemory.Service
	assetTreeSvc   *assettree.Service
	driveUploader  *drive.Uploader
	mediaProcessor asset.Processor
	deletionSvc    *deletion.DeletionService
	catalogSync    *catalogsync.Service
	maintenanceSvc *maintenance.Service
	realtimeSvc    *realtime.Service
	clipIndexer    *clipindexer.Service
	vectorStore    *qdrant.Service
	metaWriter     *semantic.MetadataWriter
	artifactSvc    *artifacts.Service
	assetRepo      asset.Repository
	log            *zap.Logger

	providerRegistry *providers.Registry

	// clips owns ALL clip routes via clipsources.Handler.RegisterRoutes.
	clips *clipsources.Handler
}

// SetRealtimeService sets the realtime service for semantic search.
func (h *SourcesHandler) SetRealtimeService(svc *realtime.Service) {
	h.realtimeSvc = svc
}

// SetClipIndexer sets the clip indexer for generating search_text/embeddings.
// Forwards to h.clips so the same instance is reachable from the unified
// clips.Handler. Late-binding after NewSourcesHandler is supported.
func (h *SourcesHandler) SetClipIndexer(ci *clipindexer.Service) {
	h.clipIndexer = ci
	if h.clips != nil {
		h.clips.SetClipIndexer(ci)
	}
}

// SetVectorStore sets the vector store for Qdrant upsert after indexing.
// Forwards to h.clips (same reach from clips.Handler).
func (h *SourcesHandler) SetVectorStore(vs *qdrant.Service) {
	h.vectorStore = vs
	if h.clips != nil {
		h.clips.SetVectorStore(vs)
	}
}

// SetMetaWriter sets the unified metadata writer for semantic enrichment.
// Forwards to h.clips and to SoundEffect (legacy in-package wiring).
func (h *SourcesHandler) SetMetaWriter(mw *semantic.MetadataWriter) {
	h.metaWriter = mw
	// PR4: SoundEffect handler moved to api/assets/soundeffect/; its
	// SetMetaWriter is called directly from WireAssets in module_assets.go.
	if h.clips != nil {
		h.clips.SetMetaWriter(mw)
	}
}

// SetArtifactService sets the artifact service for content-addressed file drive.
// Forwards to h.clips so UploadVideoClip's content-addressed blob storage
// sees the late-bound service. Same late-binding semantics as the other
// Set* setters that delegate.
func (h *SourcesHandler) SetArtifactService(svc *artifacts.Service) {
	h.artifactSvc = svc
	if h.clips != nil {
		h.clips.SetArtifactSvc(svc)
	}
}

// SetAssetRepo sets the canonical asset repository (replaces assets.ClipsRepository
// for handlers that have been migrated to use asset.Asset directly). The
// assetRepo lives only on SourcesHandler because clips.Handler received it
// at construction time via Deps — late rebinding here does NOT update
// h.clips (the clips.Handler assetRepo is unchanged). Callers that need to
// rewire asset.Repository across both must rebuild h.clips; in practice
// assetRepo is set once at startup so this limitation is theoretical.
func (h *SourcesHandler) SetAssetRepo(repo asset.Repository) {
	h.assetRepo = repo
}

// SetVoiceoverRepo sets the voiceover repository. Forwards to h.clips so
// DownloadClip's voiceover-source branch sees the late-bound repo. Both
// handlers hold the same reference; switching repos mid-process picks up
// on the next handler invocation.
func (h *SourcesHandler) SetVoiceoverRepo(repo *assets.VoiceoversRepository) {
	h.voiceoverRepo = repo
	if h.clips != nil {
		h.clips.SetVoiceoverRepo(repo)
	}
}

// SetImagesRepo sets the images repository. clips.Handler reads it in
// ListClips for the "source=images" branch, so this setter forwards to
// h.clips as well as Self.imagesRepo (legacy sources/ search_handlers.go,
// semantic_handlers.go also use it directly).
func (h *SourcesHandler) SetImagesRepo(repo *assets.ImagesRepository) {
	h.imagesRepo = repo
	if h.clips != nil {
		h.clips.SetImagesRepo(repo)
	}
}

// SetFolderMemSvc sets the folder memory service. clips.Handler reads it
// in RegenerateManifest for the folder heuristic. sources/ search_handlers.go
// also uses folderMemSvc directly for legacy path resolution.
func (h *SourcesHandler) SetFolderMemSvc(svc *foldermemory.Service) {
	h.folderMemSvc = svc
	if h.clips != nil {
		h.clips.SetFolderMemSvc(svc)
	}
}

// NewSourcesHandler creates a new common media handler.
//
// PR4 (June 2026): voiceoverSvc and voiceoverSync params removed —
// voiceover handler lives in api/assets/voiceover/ now.
func NewSourcesHandler(
	cfg *config.Config,
	jobsSvc *jobservice.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
	artlistRepo, clipsRepo, stockRepo *assets.ClipsRepository,
	folderMemSvc *foldermemory.Service,
	assetTreeSvc *assettree.Service,
	driveUploader *drive.Uploader,
	mediaProcessor asset.Processor,
	deletionSvc *deletion.DeletionService,
	catalogSync *catalogsync.Service,
	maintenanceSvc *maintenance.Service,
	providerRegistry *providers.Registry,
	log *zap.Logger,
) *SourcesHandler {
	h := &SourcesHandler{
		cfg:              cfg,
		jobsSvc:          jobsSvc,
		catalogRepo:      catalogRepo,
		assetIndexSvc:    assetIndexSvc,
		artlistRepo:      artlistRepo,
		clipsRepo:        clipsRepo,
		stockRepo:        stockRepo,
		folderMemSvc:     folderMemSvc,
		assetTreeSvc:     assetTreeSvc,
		driveUploader:    driveUploader,
		mediaProcessor:   mediaProcessor,
		deletionSvc:      deletionSvc,
		catalogSync:      catalogSync,
		maintenanceSvc:   maintenanceSvc,
		providerRegistry: providerRegistry,
		log:              log,
	}

	// PR-A Phase 4 BULK: build the unified clips.Handler with all 15 deps.
	h.clips = clipsources.NewHandler(clipsources.Deps{
		AssetRepo:      h.assetRepo,
		ClipsRepo:      h.clipsRepo,
		StockRepo:      h.stockRepo,
		ArtlistRepo:    h.artlistRepo,
		DeletionSvc:    h.deletionSvc,
		DriveUploader:  h.driveUploader,
		MediaProcessor: h.mediaProcessor,
		AssetTreeSvc:   h.assetTreeSvc,
		MetaWriter:     h.metaWriter,
		ClipIndexer:    h.clipIndexer,
		VectorStore:    h.vectorStore,
		JobsSvc:        h.jobsSvc,
		Cfg:            h.cfg,
		Log:            h.log,
		VoiceoverRepo:  h.voiceoverRepo,
		ImagesRepo:     h.imagesRepo,
		ArtifactSvc:    h.artifactSvc,
		FolderMemSvc:   h.folderMemSvc,
	})

	if jobsSvc != nil {
		if err := h.RegisterJobHandlers(); err != nil {
			log.Warn("failed to register sources job handlers", zap.Error(err))
		}
	}

	return h
}

// RegisterJobHandlers delegates to h.clips.RegisterJobHandlers.
// Kept on SourcesHandler so composition sites (init_core.go /
// compose_media.go) keep their existing h.RegisterJobHandlers() call
// shape without churn.
func (h *SourcesHandler) RegisterJobHandlers() error {
	if h.clips == nil {
		return nil
	}
	return h.clips.RegisterJobHandlers()
}

// Clips exposes the embedded clips.Handler for callers (mainly tests /
// integration suites) that want to drive clip routes through the same
// receiver the router uses. Returns nil if NewSourcesHandler has not run.
func (h *SourcesHandler) Clips() *clipsources.Handler {
	return h.clips
}

// RegisterRoutes registers non-clip media routes.
//
// PR4 (June 2026): Voiceover, SoundEffect, and RegisterFromYouTube
// routes migrated to api/assets/. SyncDriveFolder and LocalToDrive
// remain as legacy wrappers.
func (h *SourcesHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering common media routes")

	// Clip routes — single delegation to the unified clips.Handler.
	if h.clips != nil {
		h.clips.RegisterRoutes(r)
	}

	// ── PR 3: search, recommend routes migrated to api/assets/search/ ──
	// ── PR 3: diagnostics, qdrant routes migrated to api/assets/diagnostics/ ──
	// ── PR 3: drive ops routes migrated to api/assets/storage/ ──
	// ── PR 4: voiceover, soundeffect, register-from-youtube migrated to api/assets/ ──

	// Bulk upload local .mp4 folders to Drive (async job).
	if h.clips != nil {
		r.POST("/bulk-upload-youtube-clips", h.clips.BulkUploadYouTubeClips)
	}

	// Sync any Drive folder recursively into DB + asset tree + Qdrant.
	r.POST("/sync-drive-folder", h.SyncDriveFolder)

	// Upload local .mp4 + metadata.json to Drive (async job).
	r.POST("/local-to-drive", h.LocalToDrive)
}
