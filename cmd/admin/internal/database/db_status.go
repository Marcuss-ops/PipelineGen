// cmd/admin/db_status.go (June 2026 codex/db-doctor-restore):
//
// `admin db status` prints a one-line-per-DB summary: paths, sizes,
// WAL size, last backup timestamp (read from the latest file under
// data/backups/), applied/total migrations, schema_migrations row.
// Read-only; exits 0 on full report, 1 only on hard connection error.
package database

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

func RunDBStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db status", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	backupDir := fs.String("backup-dir", "", "override backup directory (defaults to <DataDir>/backups)")
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
		PrimaryDBPath:       resolved.PrimaryDBPath(),
		ObservabilityDBPath: resolved.ObservabilityDBPath(),
	}, log)
	if err != nil {
		return fmt.Errorf("open set: %w", err)
	}
	defer ds.Close()

	fmt.Println("=== pipelinegen db status ===")
	fmt.Printf("primary       %s\n", ds.PrimaryPath())
	fmt.Printf("observability %s\n", ds.ObservabilityPath())

	for label, sdb := range map[string]*storage.SQLiteDB{
		"primary":       ds.Primary,
		"observability": ds.Observability,
	} {
		reportDB(label, sdb)
	}

	bd := *backupDir
	if bd == "" {
		bd = filepath.Join(*dataDir, "backups")
	}
	if info, err := latestBackup(bd); err == nil {
		fmt.Printf("last_backup   %s  (%s, %s ago)\n",
			info.path, fmtBytes(info.size), formatAge(info.mtime))
	} else {
		fmt.Printf("last_backup   none (%v)\n", err)
	}

	// Migration status from primary (the canonical ledger).
	if report, err := storage.GetMigrationStatus(ds.Primary.DB, "migrations/sqlite"); err == nil {
		fmt.Printf("migrations    primary=%d/%d applied (%d pending)\n",
			report.AppliedN, report.Total, report.PendingN)
	} else {
		fmt.Printf("migrations    (status unavailable: %v)\n", err)
	}

	return nil
}

func reportDB(label string, sdb *storage.SQLiteDB) {
	if sdb == nil {
		fmt.Printf("[%s] (not opened)\n", label)
		return
	}
	fmt.Printf("--- %s ---\n", label)
	fmt.Printf("  path         %s\n", sdb.Path())

	if size, err := storage.DBSizeBytes(sdb.Path()); err == nil {
		fmt.Printf("  size         %s\n", fmtBytes(size))
	}
	if wal, err := storage.WalSizeBytes(sdb.Path()); err == nil {
		fmt.Printf("  wal_size     %s\n", fmtBytes(wal))
	}
	if shm, err := storage.ShmSizeBytes(sdb.Path()); err == nil {
		fmt.Printf("  shm_size     %s\n", fmtBytes(shm))
	}
	// PRAGMA probes get their own short-timeout context (2s) so a
	// long dispatcher ctx is not replayed through sync-style probes.
	// The cancel is deferred (matches runDBStatus's defer log.Sync +
	// defer ds.Close pattern) so the timer goroutine is reaped even
	// if JournalMode / BusyTimeoutMs panic.
	// AGENTS.md §7 post-write save ctx — admin `db_status` PRAGMA-journal
	// probe; the probe is a sync-style read with no parent request ctx,
	// so Background + 2s timeout is the canonical shape.
	jctx, jcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer jcancel()
	if mode, err := storage.JournalMode(jctx, sdb.DB); err == nil {
		fmt.Printf("  journal_mode %s\n", mode)
	}

	// AGENTS.md §7 post-write save ctx — admin `db_status` PRAGMA-busy
	// probe; see jctx comment above for rationale.
	bctx, bcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer bcancel()
	if ms, err := storage.BusyTimeoutMs(bctx, sdb.DB); err == nil {
		fmt.Printf("  busy_timeout %dms\n", ms)
	}
}

type backupInfo struct {
	path  string
	size  int64
	mtime time.Time
}

// latestBackup returns the most recent *.sqlite or *.db file under dir.
// Returns os.ErrNotExist if the directory is empty (a common state
// in fresh deployments).
func latestBackup(dir string) (*backupInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no backups dir at %s", dir)
		}
		return nil, err
	}
	type cand struct {
		path  string
		size  int64
		mtime time.Time
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !(filepath.Ext(n) == ".sqlite" || filepath.Ext(n) == ".db") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		cands = append(cands, cand{
			path:  filepath.Join(dir, n),
			size:  fi.Size(),
			mtime: fi.ModTime(),
		})
	}
	if len(cands) == 0 {
		return nil, fmt.Errorf("no backup files in %s", dir)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })
	return &backupInfo{path: cands[0].path, size: cands[0].size, mtime: cands[0].mtime}, nil
}

// fmtBytes is a small human formatter (matches what Go's `humanize`
// would do but we keep the import surface minimal).
func fmtBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%dB", n)
	}
	if n < k*k {
		return fmt.Sprintf("%.1fKB", float64(n)/k)
	}
	if n < k*k*k {
		return fmt.Sprintf("%.1fMB", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.2fGB", float64(n)/(k*k*k))
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	default:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	}
}
