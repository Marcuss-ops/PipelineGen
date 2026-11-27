// cmd/admin/internal/database/db_migrate.go (DB-03 follow-up, 2026-11-27):
//
// `admin db migrate` is the operator entry point for APPLYING the SQLite
// migration ledger. Before it existed, applying a pending migration to the live
// planes meant a temporary Go program calling sqlite.RunMigrationsOnDB — not
// something an operator can audit, repeat, or find in the command list.
//
// It opens the full DatabaseSet (primary, observability, cache, jobs) and runs
// the canonical runner per plane, so `-- database:` scope headers, the baseline
// sentinel and the boot-time declared-object verification behave exactly as
// they do when the service starts. `--dry-run` prints what would run and the
// post-apply pass fails if anything is still pending.
package database

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

type migratePlane struct {
	name string
	db   *storage.SQLiteDB
	dir  string
}

func RunDBMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db migrate", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	planesFlag := fs.String("planes", "primary,observability,cache,jobs", "comma-separated planes to migrate (primary,observability,cache,jobs)")
	dryRun := fs.Bool("dry-run", false, "print the pending migrations per plane and exit without applying")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	fullCfg, err := config.Get()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if *dataDir != "" && *dataDir != "./data" {
		fullCfg.Storage.DataDir = *dataDir
	}
	resolved := fullCfg.Storage.ToDatabaseStorageConfig()
	log, _ := zap.NewProduction()
	defer func() { _ = log.Sync() }()

	ds, err := storage.OpenSet(storage.StorageConfig{
		DataDir:             resolved.DataDir(),
		ObservabilityDBPath: resolved.ObservabilityDBPath(),
		CacheDBPath:         filepath.Join(resolved.DataDir(), "cache", "cache.db.sqlite"),
		JobsDBPath:          filepath.Join(resolved.DataDir(), "jobs", "jobs.db.sqlite"),
	}, log)
	if err != nil {
		return fmt.Errorf("open set: %w", err)
	}
	defer func() { _ = ds.Close() }()

	sqliteDir := storage.MigrationsDir()
	selected, err := selectMigratePlanes(*planesFlag, []migratePlane{
		{name: "primary", db: ds.Primary, dir: sqliteDir},
		{name: "observability", db: ds.Observability, dir: sqliteDir},
		{name: "cache", db: ds.Cache, dir: sqliteDir},
		{name: "jobs", db: ds.Jobs, dir: storage.JobsMigrationsDir()},
	})
	if err != nil {
		return err
	}

	pendingTotal := 0
	for _, p := range selected {
		report, err := storage.GetMigrationStatusForPlane(p.db.DB, p.dir, p.name)
		if err != nil {
			return fmt.Errorf("%s: migration status: %w", p.name, err)
		}
		fmt.Printf("== %s (%s): %d applied, %d pending\n", p.name, p.db.Path(), report.AppliedN, report.PendingN)
		for _, m := range report.Pending {
			fmt.Printf("   pending %03d %s\n", m.Version, m.Filename)
		}
		pendingTotal += report.PendingN
	}

	if *dryRun {
		fmt.Printf("\ndry run: %d pending migration(s) across %d plane(s); nothing applied\n", pendingTotal, len(selected))
		return nil
	}
	if pendingTotal == 0 {
		fmt.Println("\nnothing to apply")
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	fmt.Printf("\napplying %d pending migration(s) across %d plane(s)\n", pendingTotal, len(selected))
	for _, p := range selected {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.db.RunMigrations(log, p.dir, p.name); err != nil {
			return fmt.Errorf("migrate %s: %w", p.name, err)
		}
	}

	// The runner skips out-of-scope files and baseline-covered versions, so a
	// truthful post-check is the scoped status, not the raw ledger count.
	var stillPending int
	for _, p := range selected {
		report, err := storage.GetMigrationStatusForPlane(p.db.DB, p.dir, p.name)
		if err != nil {
			return fmt.Errorf("%s: post-apply status: %w", p.name, err)
		}
		if report.PendingN == 0 {
			continue
		}
		stillPending += report.PendingN
		fmt.Printf("== %s still has %d pending migration(s):\n", p.name, report.PendingN)
		for _, m := range report.Pending {
			fmt.Printf("   pending %03d %s\n", m.Version, m.Filename)
		}
	}
	if stillPending > 0 {
		return fmt.Errorf("%d migration(s) still pending after apply", stillPending)
	}
	fmt.Println("\nall selected planes are up to date")
	return nil
}

// selectMigratePlanes resolves the --planes list, rejecting unknown names and
// planes whose database was not opened (a missing cache handle is best-effort at
// OpenSet, but an explicit request to migrate it must not be silently skipped).
func selectMigratePlanes(spec string, all []migratePlane) ([]migratePlane, error) {
	byName := make(map[string]migratePlane, len(all))
	for _, p := range all {
		byName[p.name] = p
	}
	var selected []migratePlane
	seen := map[string]bool{}
	for _, raw := range strings.Split(spec, ",") {
		name := strings.TrimSpace(strings.ToLower(raw))
		if name == "" {
			continue
		}
		p, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("unknown plane %q (expected primary, observability, cache, jobs)", name)
		}
		if p.db == nil {
			return nil, fmt.Errorf("plane %q is not available: its database was not opened", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		selected = append(selected, p)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no planes selected (--planes is empty)")
	}
	return selected, nil
}
