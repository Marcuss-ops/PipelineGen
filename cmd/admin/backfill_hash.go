package main

import (
	"crypto/md5"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Wave C (June 2026): runBackfillHashV2 retires the legacy `--db`
	// flag and the inline `google.DefaultClient + driveapi.New` setup.
	// The subcommand now uses the canonical composition root
	// (app.InitComposition) so authentication, logging, and Drive
	// lifecycle match the rest of the admin tool surface. The canonical
	// Reader.GetFileMD5 port serves the same query (Files.Get with the
	// md5Checksum field) but with the unified auth footprint.
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	slog := log.Sugar()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("failed to initialize composition root: %w", err)
	}
	defer rootCleanup()

	if root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("drive reader port is not available (Drive auth failed or disabled)")
	}

	db := root.DB.DB
	rows, err := db.Query("SELECT id, drive_link FROM media_assets WHERE file_hash IS NULL OR file_hash = '' AND drive_link IS NOT NULL AND drive_link != ''")
	if err != nil {
		return fmt.Errorf("failed to query media_assets: %w", err)
	}
	defer rows.Close()

	ctx := cmdContext()
	driveReader := root.Drive.Reader

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

		md5sum, err := driveReader.GetFileMD5(ctx, fileID)
		if err != nil {
			slog.Errorf("failed to get MD5 for %s: %v", id, err)
			continue
		}
		if md5sum == "" {
			continue
		}

		if _, err := db.Exec("UPDATE media_assets SET file_hash=? WHERE id=?", md5sum, id); err != nil {
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
