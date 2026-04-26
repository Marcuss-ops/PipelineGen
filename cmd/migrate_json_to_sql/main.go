package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

type clipJSONRecord struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Filename     string   `json:"filename"`
	FolderID     string   `json:"folder_id"`
	FolderPath   string   `json:"folder_path"`
	Group        string   `json:"group"`
	MediaType    string   `json:"media_type"`
	DriveLink    string   `json:"drive_link"`
	DownloadLink string   `json:"download_link"`
	Tags         []string `json:"tags"`
}

type clipJSONIndex struct {
	Clips []clipJSONRecord `json:"clips"`
}

type artlistClipItem struct {
	ClipID     string   `json:"clip_id"`
	FolderID   string   `json:"folder_id"`
	Filename   string   `json:"filename"`
	Title      string   `json:"title"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	DriveURL   string   `json:"drive_url"`
	Folder     string   `json:"folder"`
	Category   string   `json:"category"`
	Source     string   `json:"source"`
	Tags       []string `json:"tags"`
	Duration   int      `json:"duration"`
	Downloaded bool     `json:"downloaded"`
}

type artlistIndex struct {
	Clips []artlistClipItem `json:"clips"`
}

type driveCheckpointEntry struct {
	Keyword  string `json:"keyword"`
	Status   string `json:"status"`
	DriveID  string `json:"drive_id"`
	DriveURL string `json:"drive_url"`
	Filename string `json:"filename"`
}

type driveCheckpointIndex struct {
	Jobs []driveCheckpointEntry `json:"jobs"`
}

func main() {
	jsonPath := flag.String("json", "./data/clip_index.json", "Path to clip_index.json")
	artlistPath := flag.String("artlist", "./data/artlist_stock_index.json", "Path to artlist_stock_index.json")
	drivePath := flag.String("drive", "./data/clipsearch_checkpoints.json", "Path to clipsearch_checkpoints.json")
	dbDir := flag.String("db-dir", "./data", "Directory for database")
	flag.Parse()

	logger, _ := zap.NewDevelopment()
	
	// Open DB
	stockDB, err := storage.NewSQLiteDB(*dbDir, "stock.db.sqlite", logger)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	defer stockDB.Close()

	// Ensure migrations are run
	migrationsDir := filepath.Join("migrations", "sqlite")
	if err := stockDB.RunMigrations(logger, migrationsDir); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	repo := clips.NewRepository(stockDB.DB)
	ctx := context.Background()

	// 1. Migrate clip_index.json
	if data, err := os.ReadFile(*jsonPath); err == nil {
		var index clipJSONIndex
		if err := json.Unmarshal(data, &index); err == nil {
			fmt.Printf("Migrating %d clips from %s...\n", len(index.Clips), *jsonPath)
			count := 0
			for _, c := range index.Clips {
				clip := &models.Clip{
					ID:           c.ID,
					Name:         c.Name,
					Filename:     c.Filename,
					FolderID:     c.FolderID,
					FolderPath:   c.FolderPath,
					Group:        c.Group,
					MediaType:    c.MediaType,
					DriveLink:    c.DriveLink,
					DownloadLink: c.DownloadLink,
					Tags:         c.Tags,
					Source:       "local",
				}
				if err := repo.UpsertClip(ctx, clip); err == nil {
					count++
				}
			}
			fmt.Printf("Successfully migrated %d clips.\n", count)
		}
	} else if data, err := os.ReadFile(*jsonPath + ".bak"); err == nil {
		// Try .bak if main is gone
		var index clipJSONIndex
		if err := json.Unmarshal(data, &index); err == nil {
			fmt.Printf("Migrating %d clips from %s.bak...\n", len(index.Clips), *jsonPath)
			count := 0
			for _, c := range index.Clips {
				clip := &models.Clip{
					ID:           c.ID,
					Name:         c.Name,
					Filename:     c.Filename,
					FolderID:     c.FolderID,
					FolderPath:   c.FolderPath,
					Group:        c.Group,
					MediaType:    c.MediaType,
					DriveLink:    c.DriveLink,
					DownloadLink: c.DownloadLink,
					Tags:         c.Tags,
					Source:       "local",
				}
				if err := repo.UpsertClip(ctx, clip); err == nil {
					count++
				}
			}
			fmt.Printf("Successfully migrated %d clips.\n", count)
		}
	}

	// 2. Migrate artlist_stock_index.json
	if data, err := os.ReadFile(*artlistPath); err == nil {
		var index artlistIndex
		if err := json.Unmarshal(data, &index); err == nil {
			fmt.Printf("Migrating %d clips from %s...\n", len(index.Clips), *artlistPath)
			count := 0
			for _, c := range index.Clips {
				id := c.ClipID
				if id == "" {
					id = c.Filename
				}
				clip := &models.Clip{
					ID:           id,
					Name:         c.Title,
					Filename:     c.Filename,
					FolderID:     c.FolderID,
					FolderPath:   c.Folder,
					Group:        c.Category,
					MediaType:    "stock",
					DriveLink:    c.DriveURL,
					ExternalURL:  c.URL,
					Tags:         c.Tags,
					Source:       c.Source,
					Category:     c.Category,
					Duration:     c.Duration,
				}
				if err := repo.UpsertClip(ctx, clip); err == nil {
					count++
				}
			}
			fmt.Printf("Successfully migrated %d Artlist clips.\n", count)
		}
	}

	// 3. Migrate clipsearch_checkpoints.json
	if data, err := os.ReadFile(*drivePath); err == nil {
		var index driveCheckpointIndex
		if err := json.Unmarshal(data, &index); err == nil {
			fmt.Printf("Migrating %d drive jobs from %s...\n", len(index.Jobs), *drivePath)
			count := 0
			for _, j := range index.Jobs {
				clip := &models.Clip{
					ID:          j.DriveID,
					Name:        j.Keyword,
					Filename:    j.Filename,
					MediaType:   "drive",
					DriveLink:   j.DriveURL,
					ExternalURL: j.DriveURL,
					Tags:        []string{j.Keyword},
					Source:      "drive",
					Metadata:    j.Status,
				}
				if err := repo.UpsertClip(ctx, clip); err == nil {
					count++
				}
			}
			fmt.Printf("Successfully migrated %d Drive clips.\n", count)
		}
	}
}
