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
	artdrive "velox/go-master/internal/upload/drive"
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
	driveSvc, err := artdrive.NewDriveServiceFromFiles(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to init Drive service: %v", err)
	}

	// 2. Open DB and create the service used by the full pipeline
	logger, _ := zap.NewProduction()
	db, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "artlist.db.sqlite", logger)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	repo := clips.NewRepository(db.DB)
	artlistSvc, err := artlistservice.NewService(
		cfg,
		db.DB,
		"",
		cfg.Paths.NodeScraperDir,
		repo,
		driveSvc,
		artdrive.ResolveArtlistRootFolderID(cfg),
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
