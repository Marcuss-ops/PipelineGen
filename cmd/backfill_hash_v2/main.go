package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"go.uber.org/zap"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/oauth2/google"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/storage"
)

func main() {
	dbPath := "/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/data/artlist.db.sqlite"

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	sqliteDB, err := storage.OpenSQLiteDB(dbPath, logger)
	if err != nil {
		log.Fatal("failed to open database:", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	ctx := context.Background()
	client, err := google.DefaultClient(ctx, driveapi.DriveScope)
	if err != nil {
		log.Fatal("failed to create drive client:", err)
	}

	driveService, err := driveapi.New(client)
	if err != nil {
		log.Fatal("failed to create drive service:", err)
	}

	rows, err := db.Query("SELECT id, drive_link FROM clips WHERE file_hash='' AND drive_link!=''")
	if err != nil {
		log.Fatal("failed to query clips:", err)
	}
	defer rows.Close()

	updated := 0
	for rows.Next() {
		var id, driveLink string
		if err := rows.Scan(&id, &driveLink); err != nil {
			continue
		}

		fileID := extractFileID(driveLink)
		if fileID == "" {
			continue
		}

		file, err := driveService.Files.Get(fileID).Fields("md5Checksum").Context(ctx).Do()
		if err != nil {
			log.Printf("failed to get checksum for %s: %v", id, err)
			continue
		}

		if file.Md5Checksum == "" {
			continue
		}

		_, err = db.Exec("UPDATE clips SET file_hash=? WHERE id=?", file.Md5Checksum, id)
		if err != nil {
			log.Printf("failed to update hash for %s: %v", id, err)
			continue
		}

		updated++
		if updated%10 == 0 {
			fmt.Printf("Updated %d clips\n", updated)
		}
	}

	fmt.Printf("Done. Updated %d clips with file_hash\n", updated)
}

func extractFileID(link string) string {
	if idx := strings.Index(link, "/d/"); idx != -1 {
		start := idx + 3
		end := strings.Index(link[start:], "/")
		if end == -1 {
			return link[start:]
		}
		return link[start : start+end]
	}
	return ""
}
