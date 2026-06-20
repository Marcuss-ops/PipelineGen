package main

import (
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2/google"
	driveapi "google.golang.org/api/drive/v3"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
)

func runBackfillHash(args []string) error {
	fs := flag.NewFlagSet("backfill-hash", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "Path to SQLite database (absolute)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return fmt.Errorf("usage: admin backfill-hash --db <absolute-path-to-sqlite>")
	}

	log, cleanup, err := productionLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	slog := log.Sugar()

	sqliteDB, err := storage.OpenSQLiteDB(*dbPath, log)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	rows, err := db.Query("SELECT id, drive_link FROM clips WHERE file_hash='' AND drive_link!=''")
	if err != nil {
		return fmt.Errorf("failed to query clips: %w", err)
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

		hash, err := fetchAndHash(fileID)
		if err != nil {
			slog.Errorf("failed to fetch file %s: %v", id, err)
			continue
		}

		if _, err := db.Exec("UPDATE clips SET file_hash=? WHERE id=?", hash, id); err != nil {
			slog.Errorf("failed to update hash for %s: %v", id, err)
			continue
		}

		updated++
		if updated%10 == 0 {
			fmt.Printf("Updated %d clips\n", updated)
		}
	}

	fmt.Printf("Done. Updated %d clips with file_hash\n", updated)
	return nil
}

func runBackfillHashV2(args []string) error {
	fs := flag.NewFlagSet("backfill-hash-v2", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dbPath := fs.String("db", "", "Path to SQLite database (absolute)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		return fmt.Errorf("usage: admin backfill-hash-v2 --db <absolute-path-to-sqlite>")
	}

	log, cleanup, err := productionLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	slog := log.Sugar()

	sqliteDB, err := storage.OpenSQLiteDB(*dbPath, log)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer sqliteDB.Close()
	db := sqliteDB.DB

	ctx := cmdContext()
	client, err := google.DefaultClient(ctx, driveapi.DriveScope)
	if err != nil {
		return fmt.Errorf("failed to create drive client: %w", err)
	}

	driveService, err := driveapi.New(client)
	if err != nil {
		return fmt.Errorf("failed to create drive service: %w", err)
	}

	rows, err := db.Query("SELECT id, drive_link FROM clips WHERE file_hash='' AND drive_link!=''")
	if err != nil {
		return fmt.Errorf("failed to query clips: %w", err)
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
			slog.Errorf("failed to get checksum for %s: %v", id, err)
			continue
		}
		if file.Md5Checksum == "" {
			continue
		}

		if _, err := db.Exec("UPDATE clips SET file_hash=? WHERE id=?", file.Md5Checksum, id); err != nil {
			slog.Errorf("failed to update hash for %s: %v", id, err)
			continue
		}

		updated++
		if updated%10 == 0 {
			fmt.Printf("Updated %d clips\n", updated)
		}
	}

	fmt.Printf("Done. Updated %d clips with file_hash\n", updated)
	return nil
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

func fetchAndHash(fileID string) (string, error) {
	url := fmt.Sprintf("https://drive.google.com/uc?id=%s", fileID)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	h := md5.New()
	if _, err := io.Copy(h, resp.Body); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
