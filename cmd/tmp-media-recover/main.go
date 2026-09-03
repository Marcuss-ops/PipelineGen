// One-off scratch recovery probe: re-download missing local mirrors of
// Drive-hosted media assets (asset ID == Drive file ID) through the
// canonical composition root's Drive reader. Removed after use.
package main

import (
	"container/list"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}

func run() error {
	pgDSN := os.Getenv("RECOVER_PG_DSN")
	if pgDSN == "" {
		return fmt.Errorf("RECOVER_PG_DSN is required")
	}
	db, err := sql.Open("pgx", pgDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}

	cfg, err := config.Get()
	if err != nil {
		return err
	}
	log, err := zap.NewProduction()
	if err != nil {
		return err
	}
	cleanup := func() { _ = log.Sync() }
	defer cleanup()
	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Drive == nil || root.Drive.Reader == nil {
		return fmt.Errorf("Drive reader is not configured")
	}
	reader := root.Drive.Reader

	// 1. Direct fixes: assets whose local_path/uri exists now.
	// 2. Missing /tmp/love-media mirrors with same-name siblings present:
	//    re-point to the sibling (deterministic, zero network).
	// 3. Everything else: re-download from Drive (asset ID = file ID).
	rows, err := db.QueryContext(ctx, `
		SELECT a.id, COALESCE(al.uri, ''), COALESCE(a.local_path, '')
		FROM media_assets a
		LEFT JOIN asset_locations al ON al.asset_id = a.id AND al.location_kind = 'local'
		WHERE a.media_type = 'video' AND a.deleted_at = '' AND a.lifecycle_state = 'ACTIVE'
	`)
	if err != nil {
		return err
	}
	type rec struct {
		id, uri, localPath string
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.uri, &r.localPath); err != nil {
			rows.Close()
			return err
		}
		recs = append(recs, r)
	}
	rows.Close()

	fixes, downloads, failures := 0, 0, 0
	for _, r := range recs {
		path := r.uri
		if path == "" {
			path = r.localPath
		}
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			continue // file present
		}
		// Sibling re-point: same filename elsewhere under a live root.
		base := filepath.Base(path)
		alt := findSibling("/tmp/love-media", base)
		if alt != "" {
			if _, err := db.ExecContext(ctx,
				`UPDATE asset_locations SET uri = $1 WHERE asset_id = $2 AND location_kind = 'local'`, alt, r.id); err == nil {
				db.ExecContext(ctx, `UPDATE media_assets SET local_path = $1 WHERE id = $2 AND local_path <> ''`, alt, r.id)
				fmt.Printf("REPOINT %s -> %s\n", r.id, alt)
				fixes++
				continue
			}
		}
		// Re-download through the canonical Drive reader.
		dest := path
		if err := downloadTo(ctx, reader, r.id, dest); err != nil {
			fmt.Printf("DOWNLOAD-FAIL %s (%s): %v\n", r.id, dest, err)
			failures++
			continue
		}
		// Ensure the asset_locations row exists (uri may have been empty).
		if r.uri == "" {
			db.ExecContext(ctx,
				`INSERT INTO asset_locations (asset_id, location_kind, uri, is_primary) VALUES ($1, 'local', $2, true)
				 ON CONFLICT DO NOTHING`, r.id, dest)
		}
		fmt.Printf("DOWNLOADED %s -> %s\n", r.id, dest)
		downloads++
	}
	fmt.Printf("SUMMARY repointed=%d downloaded=%d failures=%d\n", fixes, downloads, failures)
	if failures > 0 {
		return fmt.Errorf("%d asset(s) could not be recovered", failures)
	}
	return nil
}

func downloadTo(ctx context.Context, reader interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
}, fileID, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	body, _, err := reader.DownloadFile(ctx, fileID)
	if err != nil {
		return err
	}
	defer body.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".recover-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	// Validate the downloaded media with ffprobe (fail closed on HTML error pages).
	if out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", tmpPath).CombinedOutput(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("ffprobe rejected download: %w: %s", err, string(out))
	}
	return os.Rename(tmpPath, dest)
}

func findSibling(root, base string) string {
	queue := list.New()
	queue.PushBack(root)
	for queue.Len() > 0 {
		el := queue.Front()
		queue.Remove(el)
		dir := el.Value.(string)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if e.IsDir() {
				queue.PushBack(p)
				continue
			}
			if e.Name() == base {
				return p
			}
		}
	}
	return ""
}
