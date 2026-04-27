package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
	"go.uber.org/zap"
)

func main() {
	cfg := config.Get()
	ctx := context.Background()

	// 1. Open Artlist Scraper DB
	artlistDBPath := filepath.Join(cfg.Paths.NodeScraperDir, "artlist_videos.db")
	artlistDB, err := sql.Open("sqlite3", artlistDBPath)
	if err != nil {
		log.Fatalf("Failed to open artlist db: %v", err)
	}
	defer artlistDB.Close()

	// 2. Open Main DB
	logger, _ := zap.NewProduction()
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "velox.db.sqlite", logger)
	if err != nil {
		log.Fatalf("Failed to open main db: %v", err)
	}
	repo := clips.NewRepository(mainDB.DB)

	// 3. Query clips from scraper DB
	rows, err := artlistDB.Query(`
		SELECT v.video_id, v.file_name, v.url, s.term
		FROM video_links v
		JOIN search_terms s ON v.search_term_id = s.id
	`)
	if err != nil {
		log.Fatalf("Failed to query scraper db: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var vid, name, url, term string
		if err := rows.Scan(&vid, &name, &url, &term); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}

		clip := &models.Clip{
			ID:           vid,
			Name:         name,
			ExternalURL:  url,
			DownloadLink: url,
			Source:       "artlist",
			Category:     "dynamic",
			Tags:         []string{term},
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}

		if err := repo.UpsertClip(ctx, clip); err != nil {
			log.Printf("Error upserting clip %s: %v", vid, err)
		} else {
			count++
		}
	}

	fmt.Printf("✅ Imported %d clips from Artlist Scraper DB to Main DB\n", count)
}
