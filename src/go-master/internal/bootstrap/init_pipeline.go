package bootstrap

import (
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/clip"
	"velox/go-master/internal/api/handlers/script"
	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/imagesasset"
	"velox/go-master/internal/imagesdb"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/internal/stockjit"
	"velox/go-master/pkg/config"
)

// PipelineDeps holds the script generation, clip matching, and async pipeline services/handlers.
type PipelineDeps struct {
	ClipApprovalHandler    *clip.ClipApprovalHandler
	ScriptDocsHandler      *script.ScriptDocsHandler
	ArtlistIdx             *scriptdocs.ArtlistIndex
	ArtlistDB              *artlistdb.ArtlistDB
	ClipSearch             *clipsearch.Service
}

// initPipeline initializes the script generation pipelines: script+clips,
// script-from-clips, async pipeline, clip approval, script docs, and artlist pipeline.
func initPipeline(
	cfg *config.Config, log *zap.Logger, core *CoreDeps, clips *ClipDeps, drive *DriveDeps,
) (*PipelineDeps, []runtime.BackgroundService, CleanupFunc, error) {
	var pipelineBgSvcs []runtime.BackgroundService
	var cleanup CleanupFunc

	driveClient := drive.DriveHandler.GetDriveClient()
	docClient := drive.DriveHandler.GetDocClient()

	// === Clip Approval ===
	clipApprovalHandler := clip.NewClipApprovalHandler(core.NvidiaClient, clips.StockDB, log)

	// === Artlist Index & DB ===
	var artlistIdx *scriptdocs.ArtlistIndex
	var artlistDB *artlistdb.ArtlistDB
	var clipSearch *clipsearch.Service
	artlistIndexPath := cfg.Storage.DataDir + "/artlist_stock_index.json"
	if idx, err := scriptdocs.LoadArtlistIndex(artlistIndexPath); err == nil {
		artlistIdx = idx
		log.Info("Artlist index loaded", zap.Int("clips", len(artlistIdx.Clips)))

		artlistDB, err = artlistdb.Open(cfg.Storage.DataDir + "/artlist_local.db.json")
		if err != nil {
			log.Warn("Failed to open ArtlistDB", zap.Error(err))
		} else {
			log.Info("ArtlistDB opened", zap.String("path", cfg.Storage.DataDir+"/artlist_local.db.json"))
		}

		if driveClient != nil {
			clipSearch = clipsearch.New(
				driveClient, clips.StockDB, artlistDB,
				cfg.GetDownloadDir(), cfg.Paths.YtDlpPath,
			)
			checkpointPath := cfg.Storage.DataDir + "/clipsearch_checkpoints.json"
			if err := clipSearch.SetCheckpointStorePath(checkpointPath); err != nil {
				log.Warn("Failed to initialize clipsearch checkpoint store",
					zap.String("path", checkpointPath),
					zap.Error(err),
				)
			}
			uploadRootID := strings.TrimSpace(os.Getenv("VELOX_DYNAMIC_CLIP_UPLOAD_ROOT"))
			if uploadRootID == "" {
				uploadRootID = strings.TrimSpace(cfg.Drive.ClipsRootFolderID)
			}
			if uploadRootID == "" {
				uploadRootID = strings.TrimSpace(cfg.Drive.StockRootFolderID)
			}
			if uploadRootID == "" {
				uploadRootID = strings.TrimSpace(cfg.Drive.ArtlistFolderID)
			}
			clipSearch.SetUploadFolderID(uploadRootID)
			if clips.ClipIndexHandler != nil {
				clipSearch.SetIndexer(clips.ClipIndexHandler.GetIndexer())
			}
			if clips.ArtlistSrc != nil {
				clipSearch.SetArtlistSource(clips.ArtlistSrc)
			}
			if core.OllamaClient != nil {
				clipSearch.SetOllamaClient(core.OllamaClient)
			}
			log.Info("Dynamic clip search service initialized",
				zap.Bool("stock_db_available", clips.StockDB != nil),
			)
		}
	}

	// === Script Docs ===
	var scriptDocsHandler *script.ScriptDocsHandler
	var imagesDB *imagesdb.ImageDB
	if docClient != nil || clips.StockDB != nil {
		imagesDBPath := cfg.Storage.DataDir + "/images_local.sqlite"
		if db, err := imagesdb.Open(imagesDBPath); err == nil {
			imagesDB = db
			log.Info("ImagesDB opened", zap.String("path", imagesDBPath))
			cleanup = func() {
				_ = imagesDB.Close()
			}
		} else {
			log.Warn("Failed to open ImagesDB", zap.Error(err))
		}
	}

	if docClient != nil {
		svc := scriptdocs.NewScriptDocServiceWithDynamicFolders(
			core.ScriptGen, docClient,
			driveClient, cfg.Drive.StockRootFolderID,
			artlistIdx, clips.StockDB, clipSearch, clips.ArtlistSrc, artlistDB,
		)
		svc.SetJITResolver(stockjit.NewResolver(clipSearch, core.OllamaClient, clips.StockDB, artlistDB, driveClient, cfg.Storage.DataDir))
		if imagesDB != nil {
			svc.SetImagesDB(imagesDB)
			svc.SetImageDownloader(imagesasset.New(filepath.Join(cfg.Storage.DataDir, "image_assets")))
		}
		scriptDocsHandler = script.NewScriptDocsHandler(svc)
		log.Info("Script Docs service initialized")
	} else if clips.StockDB != nil {
		stockFolders := convertConfigToStockFolders(cfg.Drive.GetStockFolderEntries())
		svc := scriptdocs.NewScriptDocService(
			core.ScriptGen, nil, artlistIdx, clips.StockDB, stockFolders, clipSearch, clips.ArtlistSrc, artlistDB,
		)
		svc.SetJITResolver(stockjit.NewResolver(clipSearch, core.OllamaClient, clips.StockDB, artlistDB, driveClient, cfg.Storage.DataDir))
		if imagesDB != nil {
			svc.SetImagesDB(imagesDB)
			svc.SetImageDownloader(imagesasset.New(filepath.Join(cfg.Storage.DataDir, "image_assets")))
		}
		scriptDocsHandler = script.NewScriptDocsHandler(svc)
		log.Info("Script Docs service initialized (StockDB only)")
	}

	return &PipelineDeps{
		ClipApprovalHandler:    clipApprovalHandler,
		ScriptDocsHandler:      scriptDocsHandler,
		ArtlistIdx:             artlistIdx,
		ArtlistDB:              artlistDB,
		ClipSearch:             clipSearch,
	}, pipelineBgSvcs, cleanup, nil
}
