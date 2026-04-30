package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

func main() {
	cfg := config.Get()
	ctx := context.Background()
	logger, _ := zap.NewProduction()

	// 1. Open Artlist Scraper DB
	artlistDBPath := filepath.Join(cfg.Paths.NodeScraperDir, "artlist_videos.db")
	artlistDB, err := sql.Open("sqlite3", artlistDBPath)
	if err != nil {
		log.Fatalf("Failed to open artlist db: %v", err)
	}
	defer artlistDB.Close()

	// 2. Open Main DB and run migrations
	mainDB, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "velox.db.sqlite", logger)
	if err != nil {
		log.Fatalf("Failed to open main db: %v", err)
	}
	defer mainDB.Close()

	// Run migrations for clips table
	_, exePath, _, _ := runtime.Caller(0)
	migrationsDir := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(exePath))), "internal/repository/clips/migrations")
	if err := mainDB.RunMigrations(logger, migrationsDir); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
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
		var vid sql.NullString
		var name, url, term sql.NullString
		if err := rows.Scan(&vid, &name, &url, &term); err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		if !vid.Valid || strings.TrimSpace(vid.String) == "" {
			log.Printf("Skipping row with empty video_id")
			continue
		}

		tags := []string{}
		if term.Valid && term.String != "" {
			tags = []string{term.String}
		}

		clip := &models.Clip{
			ID:           vid.String,
			Name:         name.String,
			ExternalURL:  url.String,
			DownloadLink: url.String,
			Source:       "artlist",
			Category:     "dynamic",
			Tags:         tags,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}

		if err := repo.UpsertClip(ctx, clip); err != nil {
			log.Printf("Error upserting clip %s: %v", vid.String, err)
		} else {
			count++
		}
	}

	fmt.Printf("Imported %d clips from Artlist Scraper DB to Main DB\n", count)
}
