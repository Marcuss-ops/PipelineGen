package main

import (
	"context"
	"database/sql"
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

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: sync_drive <folder_id1> [folder_id2] ...")
		os.Exit(1)
	}

	logger, _ = zap.NewProduction()

	// Open DB
	db, err := storage.NewSQLiteDB("./data", "stock.db.sqlite", logger)
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

	config, err := google.ConfigFromJSON(credsData, drive.DriveReadonlyScope)
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
	tokenSource := config.TokenSource(ctx, &token)
	client := oauth2.NewClient(ctx, tokenSource)
	driveService, err := drive.New(client)
	if err != nil {
		log.Fatalf("Failed to create Drive service: %v", err)
	}

	// Process each folder
	for _, folderID := range os.Args[1:] {
		logger.Info("Processing folder", zap.String("folder_id", folderID))
		if err := syncFolder(ctx, driveService, repo, folderID, ""); err != nil {
			logger.Error("Error syncing folder", zap.String("folder_id", folderID), zap.Error(err))
		}
	}

	logger.Info("Sync completed!")
}

func syncFolder(ctx context.Context, svc *drive.Service, repo *clips.Repository, folderID, parentPath string) error {
	// Get folder info
	folder, err := svc.Files.Get(folderID).Fields("name").Do()
	if err != nil {
		return fmt.Errorf("failed to get folder: %w", err)
	}

	currentPath := filepath.Join(parentPath, folder.Name)
	logger.Info("Syncing folder", zap.String("path", currentPath))

	// List all files recursively
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
				// Recurse into subfolder
				if err := syncFolder(ctx, svc, repo, file.Id, currentPath); err != nil {
					logger.Error("Error in subfolder", zap.String("name", file.Name), zap.Error(err))
				}
				continue
			}

			// Check if video file
			if !isVideoFile(file.MimeType, file.Name) {
				continue
			}

			mediaType := determineMediaType(currentPath)

			// Check for existing clip by folder + filename
			existing, err := repo.GetClipByFolderAndFilename(ctx, folderID, file.Name)
			clipID := uuid.New().String()
			if err == nil && existing != nil {
				clipID = existing.ID
			} else if err != nil && err != sql.ErrNoRows {
				logger.Error("Failed to check existing clip", zap.String("name", file.Name), zap.Error(err))
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

func determineMediaType(path string) string {
	lowerPath := strings.ToLower(path)
	if strings.Contains(lowerPath, "artlist") {
		return "artlist"
	}
	if strings.Contains(lowerPath, "clip") {
		return "clip"
	}
	if strings.Contains(lowerPath, "drive") {
		return "drive"
	}
	return "stock"
}
