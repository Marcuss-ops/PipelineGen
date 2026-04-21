package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers"
	artlistpipeline "velox/go-master/internal/api/handlers/artlist"
	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clipcache"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/imagesasset"
	"velox/go-master/internal/imagesdb"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/service/asyncpipeline"
	"velox/go-master/internal/service/scriptclips"
	"velox/go-master/internal/service/scriptdocs"
	"velox/go-master/pkg/config"
)

// PipelineDeps holds the script generation, clip matching, and async pipeline services/handlers.
type PipelineDeps struct {
	ScriptClipsHandler     *handlers.ScriptClipsHandler
	ScriptFromClipsHandler *handlers.ScriptFromClipsHandler
	AsyncPipelineHandler   *handlers.AsyncPipelineHandler
	ClipApprovalHandler    *handlers.ClipApprovalHandler
	ScriptDocsHandler      *handlers.ScriptDocsHandler
	ArtlistPipelineHandler *artlistpipeline.Handler
	ClipCache              *clipcache.ClipCache
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
	var clipApprovalHandler *handlers.ClipApprovalHandler
	if clips.ScriptMapper != nil {
		clipApprovalHandler = handlers.NewClipApprovalHandler(clips.ScriptMapper, core.NvidiaClient, clips.StockDB, log)
	}

	// === Script + Clips ===
	var scriptClipsHandler *handlers.ScriptClipsHandler
	var scriptClipsService *scriptclips.ScriptClipsService
	if driveClient != nil && core.StockMgr != nil {
		ollamaC := ollama.NewClient("", "")
		scriptClipsService = scriptclips.NewScriptClipsService(
			core.ScriptGen, core.EntityService, core.StockMgr, driveClient,
			cfg.GetDownloadDir(), "", "", 20, ollamaC, true, core.EdgeTTS,
		)
		scriptClipsHandler = handlers.NewScriptClipsHandler(scriptClipsService)
		log.Info("Script+Clips service initialized")
	}

	// === Async Pipeline & Clip Cache ===
	var asyncPipelineHandler *handlers.AsyncPipelineHandler
	var clipCache *clipcache.ClipCache
	if scriptClipsService != nil {
		var cacheErr error
		clipCache, cacheErr = clipcache.Open(filepath.Join(cfg.Storage.DataDir, "clip_cache.json"))
		if cacheErr != nil {
			log.Warn("Failed to open clip cache, starting fresh", zap.Error(cacheErr))
			clipCache, _ = clipcache.Open(filepath.Join(cfg.Storage.DataDir, "clip_cache.json"))
		}
		scriptClipsService.SetClipCache(clipCache)
		asyncPipelineSvc := asyncpipeline.NewAsyncPipelineService(scriptClipsService, cfg.Storage.DataDir)
		asyncPipelineHandler = handlers.NewAsyncPipelineHandler(asyncPipelineSvc)
		// Async cleanup is registered as a BackgroundService for unified lifecycle.
		// It is NOT started here — the ServiceGroup handles that.
		asyncPipelineSvcRef := asyncPipelineSvc
		clipCacheRef := clipCache
		pipelineBgSvcs = append(pipelineBgSvcs, runtime.NewServiceAdapter("AsyncCleanup",
			func(ctx context.Context) error {
				go func() {
					ticker := time.NewTicker(time.Duration(cfg.Jobs.CleanupInterval) * time.Second)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							asyncPipelineSvcRef.CleanupJobs(time.Duration(cfg.Jobs.AutoCleanupHours) * time.Hour)
							clipCacheRef.Cleanup()
						}
					}
				}()
				return nil
			},
			nil, // no explicit Stop — relies on context cancellation
		))
	}

	// === Script FROM Clips ===
	var scriptFromClipsHandler *handlers.ScriptFromClipsHandler
	if core.StockMgr != nil && clips.ClipIndexHandler != nil {
		indexer := clips.ClipIndexHandler.GetIndexer()
		if indexer != nil {
			svc := scriptclips.NewScriptFromClipsService(core.ScriptGen, core.EntityService, indexer, clips.ArtlistSrc)
			scriptFromClipsHandler = handlers.NewScriptFromClipsHandler(svc)
			log.Info("Script FROM Clips service initialized", zap.Int("indexed_clips", len(indexer.GetIndex().Clips)))
		}
	}

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
	var scriptDocsHandler *handlers.ScriptDocsHandler
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
		if imagesDB != nil {
			svc.SetImagesDB(imagesDB)
			svc.SetImageDownloader(imagesasset.New(filepath.Join(cfg.Storage.DataDir, "image_assets")))
		}
		scriptDocsHandler = handlers.NewScriptDocsHandler(svc)
		log.Info("Script Docs service initialized")
	} else if clips.StockDB != nil {
		stockFolders := convertConfigToStockFolders(cfg.Drive.GetStockFolderEntries())
		svc := scriptdocs.NewScriptDocService(
			core.ScriptGen, nil, artlistIdx, clips.StockDB, stockFolders, clipSearch, clips.ArtlistSrc, artlistDB,
		)
		if imagesDB != nil {
			svc.SetImagesDB(imagesDB)
			svc.SetImageDownloader(imagesasset.New(filepath.Join(cfg.Storage.DataDir, "image_assets")))
		}
		scriptDocsHandler = handlers.NewScriptDocsHandler(svc)
		log.Info("Script Docs service initialized (StockDB only)")
	}

	// === Artlist Pipeline ===
	var artlistPipelineHandler *artlistpipeline.Handler
	if artlistDB != nil && clips.ArtlistSrc != nil {
		ffmpegPath := "ffmpeg"
		if p := os.Getenv("FFMPEG_PATH"); p != "" {
			ffmpegPath = p
		}

		artlistClipCachePath := "data/clip_cache.json"
		artlistClipCache, artlistCacheErr := clipcache.Open(artlistClipCachePath)
		if artlistCacheErr != nil {
			log.Warn("Failed to open clip cache for artlist, starting fresh", zap.Error(artlistCacheErr))
			artlistClipCache, _ = clipcache.Open(artlistClipCachePath)
		}

		keywordPoolPath := "data/keyword_pool.json"
		keywordPool, kwErr := artlistpipeline.NewKeywordPool(keywordPoolPath)
		if kwErr != nil {
			log.Warn("Failed to initialize keyword pool", zap.Error(kwErr))
			keywordPool, _ = artlistpipeline.NewKeywordPool(keywordPoolPath)
		}

		statsStorePath := "data/video_stats.json"
		statsStore, statsErr := artlistpipeline.NewStatsStore(statsStorePath)
		if statsErr != nil {
			log.Warn("Failed to initialize stats store", zap.Error(statsErr))
			statsStore, _ = artlistpipeline.NewStatsStore(statsStorePath)
		}

		artlistPipelineHandler = artlistpipeline.New(
			clips.ArtlistSrc, artlistDB, driveClient, core.OllamaClient, artlistClipCache, keywordPool, statsStore,
			cfg.GetDownloadDir(), cfg.Paths.YtDlpPath, ffmpegPath, cfg.GetOutputDir(),
		)
		log.Info("Artlist Pipeline handler initialized")
	}

	return &PipelineDeps{
		ScriptClipsHandler:     scriptClipsHandler,
		ScriptFromClipsHandler: scriptFromClipsHandler,
		AsyncPipelineHandler:   asyncPipelineHandler,
		ClipApprovalHandler:    clipApprovalHandler,
		ScriptDocsHandler:      scriptDocsHandler,
		ArtlistPipelineHandler: artlistPipelineHandler,
		ClipCache:              clipCache,
		ArtlistIdx:             artlistIdx,
		ArtlistDB:              artlistDB,
		ClipSearch:             clipSearch,
	}, pipelineBgSvcs, cleanup, nil
}
