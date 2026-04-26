package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/models"
)

func main() {
	downloadDir := flag.String("dir", "./data/downloads", "Directory containing downloaded videos")
	dbDir := flag.String("db-dir", "./data", "Directory for database")
	flag.Parse()

	logger, _ := zap.NewProduction()
	
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

	log.Printf("🔍 Scansione directory: %s", *downloadDir)

	count := 0
	err = filepath.WalkDir(*downloadDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Supportati solo video
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".mp4" && ext != ".mkv" && ext != ".mov" && ext != ".avi" {
			return nil
		}

		relPath, _ := filepath.Rel(*downloadDir, path)
		folderPath := filepath.Dir(relPath)
		if folderPath == "." {
			folderPath = "General"
		}

		filename := d.Name()
		name := strings.TrimSuffix(filename, filepath.Ext(filename))

		record := &models.Clip{
			ID:           uuid.New().String(),
			Name:         name,
			Filename:     filename,
			FolderPath:   folderPath,
			Group:        folderPath,
			MediaType:    "clip",
			DownloadLink: "file://" + path,
			Tags:         strings.Split(strings.ToLower(name), " "),
			Source:       "local",
		}

		if err := repo.UpsertClip(ctx, record); err != nil {
			log.Printf("❌ Errore upsert clip %s: %v", name, err)
		} else {
			count++
		}
		return nil
	})

	if err != nil {
		log.Fatalf("❌ Errore durante la scansione: %v", err)
	}

	log.Printf("✅ Indicizzazione completata. %d clip aggiornate nel database SQL.", count)
}
