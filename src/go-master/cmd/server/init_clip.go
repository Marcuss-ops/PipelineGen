package main

import (
	"os"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers"
	"velox/go-master/internal/catalogdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/script"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/storage/jsondb"
	"velox/go-master/pkg/config"
)

// ClipDeps holds the clip indexing, databases, and related handlers.
type ClipDeps struct {
	StockDB          *stockdb.StockDB
	ClipDB           *clipdb.ClipDB
	CatalogDB        *catalogdb.CatalogDB
	ClipIndexStore   *jsondb.ClipIndexStore
	ArtlistSrc       *clip.ArtlistSource
	ScriptMapper     *script.Mapper
	ClipIndexHandler *handlers.ClipIndexHandler
	ClipHandler      *handlers.ClipHandler
}

// initClipSystem initializes clip indexing, databases (StockDB, ClipDB, CatalogDB),
// the Artlist source, and script mapper.
//
// The ClipScanner is created but NOT started here — it is returned as a
// BackgroundService for registration with the ServiceGroup.
func initClipSystem(cfg *config.Config, log *zap.Logger, core *CoreDeps) (*ClipDeps, []runtime.BackgroundService, error) {
	var services []runtime.BackgroundService

	// === Clip Index Store ===
	clipIndexStore, err := jsondb.NewClipIndexStore(cfg.Storage.DataDir)
	if err != nil {
		log.Warn("Failed to create clip index store", zap.Error(err))
	}
	if clipIndexStore != nil {
		backfilled, err := clipIndexStore.BackfillMediaTypes()
		if err != nil {
			log.Warn("Failed to backfill media_type", zap.Error(err))
		} else if backfilled > 0 {
			log.Info("Media type backfill completed", zap.Int("backfilled", backfilled))
		}
	}

	// === Artlist Source ===
	artlistSrc := initArtlistSource(cfg, log)

	// === Clip Index Handler & Mapper ===
	clipIndexHandler := handlers.NewClipIndexHandler(
		cfg.GetClipRootFolder(), cfg.GetCredentialsPath(), cfg.GetTokenPath(), clipIndexStore, artlistSrc,
	)

	var scriptMapper *script.Mapper
	indexer := clipIndexHandler.GetIndexer()
	if indexer != nil {
		semanticSuggester := clip.NewSemanticSuggester(indexer)
		scriptMapper = script.NewMapper(semanticSuggester, core.YouTubeClientV2, &script.MapperConfig{
			MinScore:             cfg.ClipApproval.MinScore,
			MaxClipsPerScene:     cfg.ClipApproval.MaxClipsPerScene,
			AutoApproveThreshold: cfg.ClipApproval.AutoApproveThreshold,
			EnableYouTube:        core.YouTubeClientV2 != nil,
			EnableArtlist:        artlistSrc != nil,
			RequiresApproval:     true,
		})
	}

	// === Clip Handler ===
	clipHandler := handlers.NewClipHandler(cfg.GetClipRootFolder(), cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if indexer != nil {
		clipHandler.SetIndexer(indexer)
		log.Info("Clip indexer wired to ClipHandler", zap.Int("indexed_clips", len(indexer.GetIndex().Clips)))

		// Create scanner but don't start it — register with ServiceGroup
		scanInterval := time.Duration(cfg.ClipIndex.ScanInterval) * time.Second
		scanner := clip.NewIndexScanner(indexer, clipIndexStore, scanInterval)
		clipIndexHandler.SetScanner(scanner)
		services = append(services, scanner)
	}

	// === StockDB ===
	stockDBPaths := []string{
		cfg.Storage.DataDir + "/stock.db.json",
		"src/go-master/data/stock.db.json",
		"data/stock.db.json",
	}
	var stockDB *stockdb.StockDB
	for _, stockDBPath := range stockDBPaths {
		if _, err := os.Stat(stockDBPath); err == nil {
			stockDB, err = stockdb.Open(stockDBPath)
			if err != nil {
				log.Warn("Failed to open StockDB", zap.String("path", stockDBPath), zap.Error(err))
			} else {
				log.Info("StockDB opened", zap.String("path", stockDBPath))
			}
			break
		}
	}

	// === ClipDB ===
	clipDBPath := cfg.Storage.DataDir + "/clip_index.json"
	var clipDB *clipdb.ClipDB
	if _, err := os.Stat(clipDBPath); err == nil {
		clipDB, err = clipdb.Open(clipDBPath)
		if err != nil {
			log.Warn("Failed to open ClipDB", zap.Error(err))
		} else {
			log.Info("ClipDB opened", zap.Int("clips", clipDB.GetClipCount()))
		}
	} else {
		clipDB, err = clipdb.Open(clipDBPath)
		if err == nil {
			log.Info("ClipDB created", zap.String("path", clipDBPath))
		}
	}

	// === Unified CatalogDB ===
	catalogPath := cfg.Storage.DataDir + "/clips_catalog.db"
	catalog, err := catalogdb.Open(catalogPath)
	if err != nil {
		log.Warn("Failed to open CatalogDB", zap.String("path", catalogPath), zap.Error(err))
	} else {
		log.Info("CatalogDB opened", zap.String("path", catalogPath))
	}

	return &ClipDeps{
		StockDB:          stockDB,
		ClipDB:           clipDB,
		CatalogDB:        catalog,
		ClipIndexStore:   clipIndexStore,
		ArtlistSrc:       artlistSrc,
		ScriptMapper:     scriptMapper,
		ClipIndexHandler: clipIndexHandler,
		ClipHandler:      clipHandler,
	}, services, nil
}
