package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"velox/go-master/internal/repository/clips"
	artlistservice "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"
)

func main() {
	tag := flag.String("tag", "", "Tag to process")
	limit := flag.Int("limit", 5, "Number of clips to download/upload")
	rootFolderID := flag.String("root-folder-id", "", "Drive folder ID to use as root for tag folders")
	flag.Parse()

	if *tag == "" {
		log.Fatal("tag is required")
	}

	cfg := config.Get()
	ctx := context.Background()

	// 1. Initialize Drive Client
	driveSvc, err := initDriveService(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to init Drive service: %v", err)
	}

	// 2. Open DB and create the service used by the full pipeline
	logger, _ := zap.NewProduction()
	db, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "velox.db.sqlite", logger)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	repo := clips.NewRepository(db.DB)
	artlistSvc, err := artlistservice.NewService(
		db.DB,
		"",
		cfg.Paths.NodeScraperDir,
		repo,
		driveSvc,
		resolveDriveFolderID(cfg),
		logger,
	)
	if err != nil {
		log.Fatalf("Failed to create Artlist service: %v", err)
	}

	resp, err := artlistSvc.RunTag(ctx, &artlistservice.RunTagRequest{
		Term:         *tag,
		Limit:        *limit,
		RootFolderID: *rootFolderID,
	})
	if err != nil {
		log.Fatalf("Artlist pipeline failed: %v", err)
	}

	fmt.Printf("🏁 Completed: term=%s processed=%d found=%d skipped=%d failed=%d folder=%s root=%s\n",
		resp.Term, resp.Processed, resp.Found, resp.Skipped, resp.Failed, resp.TagFolderID, resp.RootFolderID)
}

func resolveDriveFolderID(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	folderID := strings.TrimSpace(cfg.Harvester.DriveFolderID)
	if folderID == "" {
		folderID = strings.TrimSpace(cfg.Drive.ClipsRootFolder)
	}
	if folderID == "" {
		folderID = strings.TrimSpace(cfg.Drive.StockRootFolder)
	}
	return folderID
}

func initDriveService(ctx context.Context, cfg *config.Config) (*drive.Service, error) {
	credsData, err := ioutil.ReadFile(cfg.Paths.CredentialsFile)
	if err != nil {
		return nil, err
	}
	tokenData, err := ioutil.ReadFile(cfg.Paths.TokenFile)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return nil, err
	}

	oauthCfg, err := google.ConfigFromJSON(credsData, drive.DriveScope)
	if err != nil {
		return nil, err
	}

	ts := oauthCfg.TokenSource(ctx, &token)
	return drive.NewService(ctx, option.WithTokenSource(ts))
}
