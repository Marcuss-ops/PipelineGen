package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
	"github.com/google/uuid"
)

var logger *zap.Logger

type SyncConfig struct {
	DBFile    string
	FolderID  string
	MediaType string
}

var syncConfigs = map[string]SyncConfig{
	"stock_drive": {
		DBFile:    "stock_drive.db.sqlite",
		FolderID:  "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh",
		MediaType: "stock_drive",
	},
	"artlist": {
		DBFile:    "artlist.db.sqlite",
		FolderID:  "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk",
		MediaType: "artlist",
	},
	"clips": {
		DBFile:    "clips.db.sqlite",
		FolderID:  "1ID_oFJF15Q5nmiZF0d2NaJeKhsOJpQNS",
		MediaType: "clips",
	},
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: sync_drive <sync_type>")
		fmt.Println("Available sync types: stock_drive, artlist, clips")
		os.Exit(1)
	}

	syncType := os.Args[1]
	config, ok := syncConfigs[syncType]
	if !ok {
		log.Fatalf("Unknown sync type: %s", syncType)
	}

	logger, _ = zap.NewProduction()

	// Open DB
	db, err := storage.NewSQLiteDB("./data", config.DBFile, logger)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	defer db.Close()

	repo := clips.NewRepository(db.DB)
	ctx := context.Background()

	// Load credentials
	credsData, err := ioutil.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Failed to read credentials: %v", err)
	}

	cfg, err := google.ConfigFromJSON(credsData, drive.DriveReadonlyScope)
	if err != nil {
		log.Fatalf("Failed to parse credentials: %v", err)
	}

	// Load token
	tokenData, err := ioutil.ReadFile("token.json")
	if err != nil {
		log.Fatalf("Failed to read token: %v", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		log.Fatalf("Failed to parse token: %v", err)
	}

	// Create Drive client
	tokenSource := cfg.TokenSource(ctx, &token)
	client := oauth2.NewClient(ctx, tokenSource)
	driveService, err := drive.New(client)
	if err != nil {
		log.Fatalf("Failed to create Drive service: %v", err)
	}

	logger.Info("Starting sync", zap.String("type", syncType), zap.String("folder", config.FolderID))

	if err := syncFolder(ctx, driveService, repo, config.FolderID, "", config.MediaType); err != nil {
		logger.Error("Sync failed", zap.Error(err))
	}

	logger.Info("Sync completed!", zap.String("type", syncType))
}

func syncFolder(ctx context.Context, svc *drive.Service, repo *clips.Repository, folderID, parentPath, mediaType string) error {
	folder, err := svc.Files.Get(folderID).Fields("name").Do()
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}

	currentPath := filepath.Join(parentPath, folder.Name)
	logger.Info("Syncing folder", zap.String("path", currentPath))

	pageToken := ""
	for {
		query := fmt.Sprintf("'%s' in parents and trashed=false", folderID)
		call := svc.Files.List().
			Q(query).
			Fields("nextPageToken, files(id,name,mimeType,size,webViewLink)").
			PageSize(100)

		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		list, err := call.Do()
		if err != nil {
			return fmt.Errorf("failed to list files: %w", err)
		}

		for _, file := range list.Files {
			if file.MimeType == "application/vnd.google-apps.folder" {
				if err := syncFolder(ctx, svc, repo, file.Id, currentPath, mediaType); err != nil {
					logger.Error("Error in subfolder", zap.String("name", file.Name), zap.Error(err))
				}
				continue
			}

			if !isVideoFile(file.MimeType, file.Name) {
				continue
			}

			existing, err := repo.GetClipByFolderAndFilename(ctx, folderID, file.Name)
			clipID := uuid.New().String()
			if err == nil && existing != nil {
				clipID = existing.ID
			}

			clip := &models.Clip{
				ID:           clipID,
				Name:         strings.TrimSuffix(file.Name, filepath.Ext(file.Name)),
				Filename:     file.Name,
				FolderID:     folderID,
				FolderPath:   currentPath,
				Group:        folder.Name,
				MediaType:    mediaType,
				DriveLink:    file.WebViewLink,
				DownloadLink: "https://drive.google.com/uc?id=" + file.Id,
				Tags:         strings.Split(strings.ToLower(file.Name), " "),
				Source:       mediaType,
			}

			if err := repo.UpsertClip(ctx, clip); err != nil {
				logger.Error("Failed to upsert clip", zap.String("name", file.Name), zap.Error(err))
				continue
			}

			logger.Info("  ✓ synced", zap.String("name", file.Name), zap.String("type", mediaType))
		}

		if list.NextPageToken == "" {
			break
		}
		pageToken = list.NextPageToken
	}

	return nil
}

func isVideoFile(mimeType, filename string) bool {
	if strings.HasPrefix(mimeType, "video/") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".mp4" || ext == ".mkv" || ext == ".mov" || ext == ".avi"
}
