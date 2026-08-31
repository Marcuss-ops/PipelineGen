// cmd/admin/db_check.go (June 2026 codex/db-doctor-restore):
//
// `admin db check` runs the full integrity suite over BOTH the
// primary and observability databases:
//   - PRAGMA integrity_check
//   - PRAGMA foreign_key_check
//   - PRAGMA journal_mode + busy_timeout
//   - WAL size on disk
//   - Table counts (top-N by row count)
//   - Critical columns/indexes presence
//
// Exits 0 when all checks pass, 1 on any failure (prints the failing
// check). Exit-code contract matches the other admin commands.
package database

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

func RunDBCheck(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db check", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	qdrantURL := fs.String("qdrant-url", "", "optional Qdrant base URL for connectivity probe (e.g. http://127.0.0.1:6333)")
	topN := fs.Int("top-n-tables", 10, "show row counts for the top-N tables")
	fs.Parse(args)

	fullCfg, err := config.Get()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if *dataDir != "" && *dataDir != "./data" {
		fullCfg.Storage.DataDir = *dataDir
	}
	resolved := fullCfg.Storage.ToDatabaseStorageConfig()
	log, _ := zap.NewProduction()
	defer log.Sync()

	ds, err := storage.OpenSet(storage.StorageConfig{
		DataDir:             resolved.DataDir(),
		ObservabilityDBPath: resolved.ObservabilityDBPath(),
	}, log)
	if err != nil {
		return fmt.Errorf("open set: %w", err)
	}
	defer ds.Close()

	failures := 0
	if err := checkOneDB(ctx, "primary", ds.Primary, *topN, &failures); err != nil {
		fmt.Printf("[primary] FAIL: %v\n", err)
		failures++
	} else {
		fmt.Println("[primary] OK")
	}
	if err := checkOneDB(ctx, "observability", ds.Observability, *topN, &failures); err != nil {
		fmt.Printf("[observability] FAIL: %v\n", err)
		failures++
	} else {
		fmt.Println("[observability] OK")
	}

	if *qdrantURL != "" {
		if ok, dur, err := probeQdrant(*qdrantURL, 3*time.Second); err != nil {
			fmt.Printf("[qdrant] FAIL (%v, %s elapsed)\n", err, dur)
			failures++
		} else if !ok {
			fmt.Printf("[qdrant] FAIL: non-OK response (%s elapsed)\n", dur)
			failures++
		} else {
			fmt.Printf("[qdrant] OK (%s elapsed)\n", dur)
		}
	} else {
		fmt.Println("[qdrant] SKIP (no -qdrant-url provided)")
	}

	fmt.Printf("=== summary: %d failure(s) ===\n", failures)
	if failures > 0 {
		return fmt.Errorf("%d check(s) failed", failures)
	}
	return nil
}

func checkOneDB(ctx context.Context, label string, sdb *storage.SQLiteDB, topN int, failures *int) error {
	if sdb == nil {
		return fmt.Errorf("not opened")
	}

	if err := storage.IntegrityCheck(ctx, sdb.DB); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	if v, err := storage.ForeignKeyCheck(ctx, sdb.DB); err != nil {
		return fmt.Errorf("foreign_key_check: %w", err)
	} else if len(v) > 0 {
		// Schema-level FK mismatches (WARNING-prefixed) are
		// printed but NOT counted as failures — they indicate
		// a schema inconsistency, not actual data corruption.
		// Row-level violations are still hard failures.
		allWarnings := true
		for _, line := range v {
			if strings.HasPrefix(line, "WARNING:") {
				fmt.Printf("  [%s] FK schema warning: %s\n", label, line)
			} else {
				fmt.Printf("  [%s] FK violation: %s\n", label, line)
				allWarnings = false
			}
		}
		if !allWarnings {
			return fmt.Errorf("%d FK violation(s)", len(v))
		}
	}
	if mode, err := storage.JournalMode(ctx, sdb.DB); err != nil {
		return fmt.Errorf("journal_mode: %w", err)
	} else {
		fmt.Printf("  [%s] journal_mode=%s\n", label, mode)
		if mode != "wal" {
			return fmt.Errorf("journal_mode is %q, expected wal", mode)
		}
	}
	if ms, err := storage.BusyTimeoutMs(ctx, sdb.DB); err != nil {
		return fmt.Errorf("busy_timeout: %w", err)
	} else {
		fmt.Printf("  [%s] busy_timeout=%dms\n", label, ms)
	}
	if wal, err := storage.WalSizeBytes(sdb.Path()); err != nil {
		return fmt.Errorf("wal size: %w", err)
	} else {
		fmt.Printf("  [%s] wal_size=%dB\n", label, wal)
	}

	tables, err := storage.AllUserTables(ctx, sdb.DB)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	counts, err := storage.TableCounts(ctx, sdb.DB, tables)
	if err != nil {
		return fmt.Errorf("table counts: %w", err)
	}
	// Print top-N by row count.
	pairs := make([]struct {
		t string
		n int
	}, 0, len(counts))
	for t, n := range counts {
		pairs = append(pairs, struct {
			t string
			n int
		}{t, n})
	}
	// sort desc
	for i := 0; i < len(pairs); i++ {
		for j := i + 1; j < len(pairs); j++ {
			if pairs[j].n > pairs[i].n {
				pairs[i], pairs[j] = pairs[j], pairs[i]
			}
		}
	}
	shown := topN
	if shown > len(pairs) {
		shown = len(pairs)
	}
	fmt.Printf("  [%s] table_counts (top %d):\n", label, shown)
	for i := 0; i < shown; i++ {
		fmt.Printf("    %s = %d\n", pairs[i].t, pairs[i].n)
	}

	for _, col := range storage.CriticalColumns {
		ok, err := storage.ColumnExists(ctx, sdb.DB, col.Table, col.Column)
		if err != nil {
			return fmt.Errorf("column_exists(%s, %s): %w", col.Table, col.Column, err)
		}
		if !ok {
			return fmt.Errorf("critical column missing: %s.%s", col.Table, col.Column)
		}
	}
	fmt.Printf("  [%s] critical_columns: %d/%d present\n", label, len(storage.CriticalColumns), len(storage.CriticalColumns))

	if n, err := storage.IndexCount(ctx, sdb.DB); err != nil {
		return fmt.Errorf("index_count: %w", err)
	} else {
		fmt.Printf("  [%s] index_count=%d\n", label, n)
	}
	return nil
}

// probeQdrant performs a single HTTP GET against the Qdrant /healthz
// endpoint. Returns (ok, elapsed, err) so the caller can print a
// structured summary regardless of outcome. Timeout is enforced via
// the http.Client.Timeout so a hung Qdrant doesn't wedge the check.
func probeQdrant(baseURL string, timeout time.Duration) (bool, time.Duration, error) {
	if baseURL == "" {
		return false, 0, fmt.Errorf("empty base url")
	}
	healthURL := strings.TrimRight(baseURL, "/") + "/healthz"
	req, err := http.NewRequest(http.MethodGet, healthURL, nil)
	if err != nil {
		return false, 0, fmt.Errorf("build request: %w", err)
	}
	client := &http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return false, elapsed, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, elapsed, fmt.Errorf("status %d", resp.StatusCode)
	}
	return true, elapsed, nil
}
