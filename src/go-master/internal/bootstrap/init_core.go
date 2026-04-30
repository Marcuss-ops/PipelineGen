package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"velox/go-master/internal/api/handlers/common"
	"velox/go-master/internal/cron"
	"velox/go-master/internal/ml/ollama"
	"velox/go-master/internal/ml/ollama/client"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/harvester"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/scripts"
	imgservice "velox/go-master/internal/service/images"
	"velox/go-master/internal/service/indexing"
	"velox/go-master/internal/service/monitor"
	"velox/go-master/internal/service/scheduler"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/storage"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
)

// CoreDeps holds the minimal runtime dependencies needed by the stripped-down server.
type CoreDeps struct {
	ScriptGen            *ollama.Generator
	DocClient            drive.DocClient
	DriveClient          *gdrive.Service
	Utility              *common.UtilityHandler
	DB                   *storage.SQLiteDB // Unified database
	ImagesDB             *storage.SQLiteDB // Images database
	ScriptsRepo          *scripts.ScriptRepository
	ImageRepo            *images.Repository
	ImageService         *imgservice.Service
	StockDriveRepo       *clips.Repository
	ArtlistRepo          *clips.Repository
	ClipsOnlyRepo        *clips.Repository
	VoiceoverService     *voiceover.Service
	IndexingService      *indexing.Service
	HarvesterCronService *cron.HarvesterCronService
	ChannelMonitor       *monitor.ChannelMonitor
	StockScheduler       *scheduler.StockScheduler
}

func ExportInitCoreMinimal(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	return initCoreMinimal(cfg, log)
}

// initCoreMinimal creates only the services needed by the text/doc server.
func initCoreMinimal(cfg *config.Config, log *zap.Logger) (*CoreDeps, CleanupFunc, error) {
	ollamaClient := client.NewClient(cfg.External.OllamaURL, "")
	scriptGen := ollama.NewGenerator(ollamaClient)

	docClient, err := drive.NewDocClient(context.Background(), cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Docs client not initialized", zap.Error(err))
	}

	// Initialize Google Drive client for Artlist service using the same OAuth files
	driveClient, err := newGoogleDriveService(context.Background(), cfg.GetCredentialsPath(), cfg.GetTokenPath())
	if err != nil {
		log.Warn("Google Drive client not initialized", zap.Error(err))
	}

	// Initialize voiceover service
	voDir := filepath.Join(cfg.Storage.DataDir, "voiceovers")
	if err := os.MkdirAll(voDir, 0755); err != nil {
		log.Warn("Failed to create voiceovers directory", zap.Error(err))
	}
	voService := voiceover.NewService(cfg.Paths.PythonScriptsDir, voDir, log)

	// Initialize unified database with WAL mode
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "velox.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize main database: %w", err)
	}

	stockDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "stock.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize stock database: %w", err)
	}

	// Initialize images database
	imagesDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "images.db.sqlite", log)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize images database: %w", err)
	}

	// Run all migrations on the unified database
	// 1. Orchestration tables (jobs, workers, etc.)
	orchestrationMigrationsDir := filepath.Join("migrations", "sqlite")
	if err := mainDB.RunMigrations(log, orchestrationMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run orchestration migrations: %w", err)
	}

	if err := stockDB.RunMigrations(log, orchestrationMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run stock orchestration migrations: %w", err)
	}

	// 2. Scripts tables
	scriptsMigrationsDir := filepath.Join("internal", "repository", "scripts", "migrations")
	if err := mainDB.RunMigrations(log, scriptsMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run scripts migrations: %w", err)
	}

	if err := stockDB.RunMigrations(log, scriptsMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run stock scripts migrations: %w", err)
	}

	clipsMigrationsDir := filepath.Join("internal", "repository", "clips", "migrations")
	if err := mainDB.RunMigrations(log, clipsMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run clips migrations: %w", err)
	}
	if err := stockDB.RunMigrations(log, clipsMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run stock clips migrations: %w", err)
	}

	// 3. Harvester jobs table
	harvesterMigrationsDir := filepath.Join("internal", "repository", "harvester", "migrations")
	// Create dir if not exists and run migrations
	if err := os.MkdirAll(harvesterMigrationsDir, 0755); err == nil {
		if err := mainDB.RunMigrations(log, harvesterMigrationsDir); err != nil {
			log.Warn("Failed to run harvester migrations", zap.Error(err))
		}
		if err := stockDB.RunMigrations(log, harvesterMigrationsDir); err != nil {
			log.Warn("Failed to run stock harvester migrations", zap.Error(err))
		}
	}

	// 4. Images tables
	imagesMigrationsDir := filepath.Join("internal", "repository", "images", "migrations")
	if err := imagesDB.RunMigrations(log, imagesMigrationsDir); err != nil {
		return nil, nil, fmt.Errorf("failed to run images migrations: %w", err)
	}

	// Create repositories sharing the same database
	clipsRepo := clips.NewRepository(stockDB.DB)
	artlistRepo := clips.NewRepository(mainDB.DB)
	clipsOnlyRepo := clips.NewRepository(mainDB.DB)

	scriptsRepo := scripts.NewScriptRepository(mainDB.DB)
	imageRepo := images.NewRepository(imagesDB.DB)

	// Initialize images service
	imgAssetsDir := filepath.Join(cfg.Storage.DataDir, "assets", "subjects")
	if err := os.MkdirAll(imgAssetsDir, 0755); err != nil {
		log.Warn("Failed to create image assets directory", zap.Error(err))
	}
	imageService := imgservice.NewService(imageRepo, imgAssetsDir, log)

	// Initialize harvester repository and cron service
	harvesterRepo := harvester.NewRepository(mainDB.DB, log)
	apiURL := fmt.Sprintf("http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	harvesterCronSvc := cron.NewHarvesterCronService(harvesterRepo, log, apiURL, cfg.Storage.DataDir)
	go harvesterCronSvc.Start(context.Background())
	log.Info("Harvester cron service started", zap.String("api_url", apiURL))

	// Initialize channel monitor if enabled
	var channelMon *monitor.ChannelMonitor
	if os.Getenv("VELOX_ENABLE_CHANNEL_MONITOR") == "true" {
		channelMon = monitor.NewChannelMonitor(cfg, clipsRepo, log)
		go channelMon.Start(context.Background())
		log.Info("Channel monitor started")
	}

	// Initialize stock scheduler if enabled
	var stockSched *scheduler.StockScheduler
	if os.Getenv("VELOX_ENABLE_STOCK_SCHEDULER") == "true" {
		stockSched = scheduler.NewStockScheduler(cfg, log)
		go stockSched.Start(context.Background())
		log.Info("Stock scheduler started")
	}

	indexingService := indexing.NewService(clipsRepo, log)

	// Initialize and start DB maintenance cron (runs every 24 hours)
	dbMaintenanceJob := cron.NewDBMaintenanceJob(scriptsRepo, mainDB, log)
	go dbMaintenanceJob.StartCron(context.Background(), 24*time.Hour)
	log.Info("DB maintenance cron started", zap.Duration("interval", 24*time.Hour))

	// Initialize and start DB backup cron (runs every 6 hours)
	backupDir := filepath.Join(cfg.Storage.DataDir, "backups")
	dbBackupJob := cron.NewDBBackupJob(mainDB, log, backupDir)
	go dbBackupJob.StartCron(context.Background(), 6*time.Hour)
	log.Info("DB backup cron started", zap.String("backup_dir", backupDir), zap.Duration("interval", 6*time.Hour))

	// Start indexing cron (e.g., every 15 minutes)
	downloadDir := filepath.Join(cfg.Storage.DataDir, "downloads")
	indexingService.StartCron(context.Background(), downloadDir, 15*time.Minute)

	cleanup := func() {
		// Stop services
		if channelMon != nil {
			channelMon.Stop()
		}
		if stockSched != nil {
			stockSched.Stop()
		}
		if harvesterCronSvc != nil {
			harvesterCronSvc.Stop()
		}
		if imagesDB != nil {
			if err := imagesDB.Backup(); err != nil {
				log.Warn("Failed to create images backup on shutdown", zap.Error(err))
			}
			if err := imagesDB.Close(); err != nil {
				log.Error("Failed to close images database", zap.Error(err))
			}
		}
		if mainDB != nil {
			// Create a backup before closing
			if err := mainDB.Backup(); err != nil {
				log.Warn("Failed to create backup on shutdown", zap.Error(err))
			}
			if err := mainDB.Close(); err != nil {
				log.Error("Failed to close main database", zap.Error(err))
			}
		}
		if stockDB != nil {
			if err := stockDB.Backup(); err != nil {
				log.Warn("Failed to create stock backup on shutdown", zap.Error(err))
			}
			if err := stockDB.Close(); err != nil {
				log.Error("Failed to close stock database", zap.Error(err))
			}
		}
	}

	return &CoreDeps{
		ScriptGen:            scriptGen,
		DocClient:            docClient,
		DriveClient:          driveClient,
		Utility:              common.NewUtilityHandler(),
		DB:                   mainDB,
		StockDriveRepo:       clipsRepo,
		ImagesDB:             imagesDB,
		ScriptsRepo:          scriptsRepo,
		ImageRepo:            imageRepo,
		ImageService:         imageService,
		ArtlistRepo:          artlistRepo,
		ClipsOnlyRepo:        clipsOnlyRepo,
		VoiceoverService:     voService,
		IndexingService:      indexingService,
		HarvesterCronService: harvesterCronSvc,
		ChannelMonitor:       channelMon,
		StockScheduler:       stockSched,
	}, cleanup, nil
}

func newGoogleDriveService(ctx context.Context, credentialsPath, tokenPath string) (*gdrive.Service, error) {
	if credentialsPath == "" || tokenPath == "" {
		return nil, fmt.Errorf("google drive credentials/token paths are required")
	}
	if _, err := os.Stat(credentialsPath); err != nil {
		return nil, fmt.Errorf("google credentials file not found: %w", err)
	}
	if _, err := os.Stat(tokenPath); err != nil {
		return nil, fmt.Errorf("google token file not found: %w", err)
	}

	credentials, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read google credentials: %w", err)
	}
	cfg, err := google.ConfigFromJSON(credentials, gdrive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("failed to parse google credentials: %w", err)
	}

	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read google token: %w", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return nil, fmt.Errorf("failed to parse google token: %w", err)
	}

	httpClient := oauth2.NewClient(ctx, cfg.TokenSource(ctx, &token))
	if httpClient == nil {
		return nil, fmt.Errorf("failed to create google oauth client")
	}

	return gdrive.NewService(ctx, option.WithHTTPClient(httpClient))
}
