package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"velox/go-master/internal/api/handlers/catalog"
	"velox/go-master/internal/api/handlers/clip"
	"velox/go-master/internal/catalogdb"
	internalclip "velox/go-master/internal/clip"
	"velox/go-master/internal/clipdb"
	"velox/go-master/internal/runtime"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/storage/jsondb"
	"velox/go-master/pkg/config"
)

// ClipDeps holds the clip indexing, databases, and related handlers.
type ClipDeps struct {
	StockDB              *stockdb.StockDB
	ClipDB               *clipdb.ClipDB
	CatalogDB            *catalogdb.CatalogDB
	CatalogSQLiteHandler *catalog.CatalogSQLiteHandler
	ClipIndexStore       *jsondb.ClipIndexStore
	ArtlistSrc           *internalclip.ArtlistSource
	ClipIndexHandler     *clip.ClipIndexHandler
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

	// === Clip Index Handler ===
	clipIndexHandler := clip.NewClipIndexHandler(
		cfg.GetClipRootFolder(), cfg.GetCredentialsPath(), cfg.GetTokenPath(), clipIndexStore, artlistSrc,
	)

	indexer := clipIndexHandler.GetIndexer()
	if indexer != nil {
		indexer.SetScanFolderIDs(cfg.DriveScan.ClipsFolderIDs)
	}

	if indexer != nil {
		log.Info("Clip indexer initialized", zap.Int("indexed_clips", len(indexer.GetIndex().Clips)))

		// Create scanner only when Drive is available; otherwise the server
		// should still boot and serve cached data without background scans.
		if indexer.HasDriveClient() {
			scanInterval := time.Duration(cfg.ClipIndex.ScanInterval) * time.Second
			scanner := internalclip.NewIndexScanner(indexer, clipIndexStore, scanInterval)
			clipIndexHandler.SetScanner(scanner)
			services = append(services, scanner)
		} else {
			log.Warn("Clip scanner disabled because Drive client is unavailable; cached index only")
		}
	}

	// === StockDB ===
	stockDBPaths := []string{
		filepath.Join(cfg.Storage.DataDir, "stock.db.json"),
	}
	if renamed := findRenamedStockDBPath(cfg.Storage.DataDir); renamed != "" {
		alreadyListed := false
		for _, p := range stockDBPaths {
			if p == renamed {
				alreadyListed = true
				break
			}
		}
		if !alreadyListed {
			stockDBPaths = append([]string{renamed}, stockDBPaths...)
			log.Info("Detected renamed StockDB file", zap.String("path", renamed))
		}
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
	if stockDB == nil {
		defaultStockDBPath := filepath.Join(cfg.Storage.DataDir, "stock.db.json")
		stockDB, err = stockdb.Open(defaultStockDBPath)
		if err != nil {
			log.Warn("Failed to create default StockDB", zap.String("path", defaultStockDBPath), zap.Error(err))
		} else {
			log.Info("StockDB created", zap.String("path", defaultStockDBPath))
		}
	}

	// === ClipDB ===
	clipDBPath := filepath.Join(cfg.Storage.DataDir, "clip_index.json")
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
	unifiedCatalogPath := filepath.Join(cfg.Storage.DataDir, "unified_catalog.db")
	catalogDB, err := catalogdb.Open(unifiedCatalogPath)
	if err != nil {
		log.Warn("Failed to open CatalogDB", zap.String("path", unifiedCatalogPath), zap.Error(err))
	} else {
		log.Info("CatalogDB opened", zap.String("path", unifiedCatalogPath))
	}

	// === CatalogSQLite Handler (New API - Legacy Catalog) ===
	catalogPath := filepath.Join(cfg.Storage.DataDir, "clips_catalog.db")
	catalogSQLiteHandler, err := catalog.NewCatalogSQLiteHandler(catalogPath)
	if err != nil {
		log.Warn("Failed to initialize CatalogSQLiteHandler", zap.Error(err))
	}

	return &ClipDeps{
		StockDB:              stockDB,
		ClipDB:               clipDB,
		CatalogDB:            catalogDB,
		CatalogSQLiteHandler: catalogSQLiteHandler,
		ClipIndexStore:       clipIndexStore,
		ArtlistSrc:           artlistSrc,
		ClipIndexHandler:     clipIndexHandler,
	}, services, nil
}

func findRenamedStockDBPath(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return ""
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if name == "stock.db.json" {
			continue
		}
		if !strings.Contains(name, "stock") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dataDir, entry.Name())
		if isLikelyStockDBFile(path) {
			return path
		}
	}
	return ""
}

func isLikelyStockDBFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var probe struct {
		Folders []struct {
			DriveID  string `json:"drive_id"`
			FullPath string `json:"full_path"`
			Section  string `json:"section"`
		} `json:"folders"`
		Clips []struct {
			ClipID   string `json:"clip_id"`
			FolderID string `json:"folder_id"`
		} `json:"clips"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	for _, f := range probe.Folders {
		if strings.TrimSpace(f.DriveID) != "" && strings.TrimSpace(f.FullPath) != "" {
			return true
		}
	}
	for _, c := range probe.Clips {
		if strings.TrimSpace(c.ClipID) != "" && strings.TrimSpace(c.FolderID) != "" {
			return true
		}
	}
	return false
}
