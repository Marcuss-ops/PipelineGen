// Package storage — DatabaseSet: the canonical entry point for opening,
// migrating, health-checking, and closing ALL sqlite databases used by
// the PipelineGen runtime.
//
// Pre-2026 the codebase opened a single sqlite DB at
// `<DataDir>/media.db.sqlite` plus a secondary DB at `<DataDir>/logs/api_requests.db.sqlite`
// from inside `internal/app/bootstrap.go`. Both open-sites used
// `database/sql` directly. This file centralises that pattern so callers
// (notably `internal/app/composition.go`) can open the full set with one
// typed call and never see a raw `*sql.DB` outside this package.
//
// The set is split into two roles:
//
//   - Primary       — the unified media database (jobs, assets, scripts,
//     research_cache, media_assets, clip_folders,
//     search_queries, worker_nodes, voiceovers, etc.)
//     historically at `<DataDir>/media/media.db.sqlite`.
//   - Observability — the API request log database at
//     `<DataDir>/observability/api_requests.db.sqlite`.
//
// Defaults preserve the legacy single-file path
// `<DataDir>/media.db.sqlite` as the Primary DB so existing deployments
// keep working without migration. The path migration tool
// (`cmd/admin/path_migrate.go`, future PR) performs backup + SHA256
// checksum + PRAGMA integrity_check + rollback when operators opt in.
package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	_ "github.com/mattn/go-sqlite3" // SQLite3 driver (CGO)
	"go.uber.org/zap"
)

// DatabaseSet is the typed collection of every sqlite database the
// runtime opens at startup. Construct via OpenSet — never hand-roll
// raw `*sql.DB` instances outside this package.
type DatabaseSet struct {
	// Primary holds the unified media database (jobs, assets, scripts,
	// search_queries, etc.). Backs every per-feature repository in
	// `internal/infrastructure/database/sqlite/<owner>/`.
	Primary *SQLiteDB

	// Observability holds the API request log database
	// (`api_requests` table + indexes). Independent file from Primary
	// so log retention doesn't churn the schema-versioned Primary DB.
	Observability *SQLiteDB

	cfg StorageConfig
	log *zap.Logger

	mu     sync.Mutex
	closed atomic.Bool
}

// StorageConfig is the resolved storage layout passed to OpenSet. It
// mirrors `config.StorageConfig` but is package-local to avoid an
// `internal/infrastructure/database` → `internal/platform/config`
// import cycle when this package is consumed by `config`.
type StorageConfig struct {
	DataDir             string
	PrimaryDBPath       string
	ObservabilityDBPath string
	WorkspaceDir        string
	CacheDir            string
	ExportDir           string
}

// ResolveStorageConfig fills zero-valued paths in `cfg` with defaults
// that preserve the legacy single-file layout (PrimaryDBPath defaults to
// `<DataDir>/media/media.db.sqlite` to match today's on-disk file).
func ResolveStorageConfig(cfg StorageConfig) StorageConfig {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if cfg.PrimaryDBPath == "" {
		// Compat: matches today's file via DataDir/media/media.db.sqlite.
		cfg.PrimaryDBPath = filepath.Join(cfg.DataDir, "media", "media.db.sqlite")
	}
	if cfg.ObservabilityDBPath == "" {
		cfg.ObservabilityDBPath = filepath.Join(cfg.DataDir, "observability", "api_requests.db.sqlite")
	}
	if cfg.WorkspaceDir == "" {
		cfg.WorkspaceDir = filepath.Join(cfg.DataDir, "workspace")
	}
	if cfg.CacheDir == "" {
		cfg.CacheDir = filepath.Join(cfg.DataDir, "cache")
	}
	if cfg.ExportDir == "" {
		cfg.ExportDir = filepath.Join(cfg.DataDir, "export")
	}
	return cfg
}

// OpenSet opens BOTH the Primary and Observability databases with the
// canonical wal/foreign_keys/busy_timeout pragma set, runs a Ping on
// each, and returns the typed DatabaseSet. This is the ONLY entry point
// in the runtime that creates a `*sql.DB` — every other consumer calls
// `set.Primary` or `set.Observability`.
func OpenSet(cfg StorageConfig, log *zap.Logger) (*DatabaseSet, error) {
	if log == nil {
		log = zap.NewNop()
	}
	cfg = ResolveStorageConfig(cfg)

	primary, err := NewSQLiteDB(filepath.Dir(cfg.PrimaryDBPath), filepath.Base(cfg.PrimaryDBPath), log)
	if err != nil {
		return nil, fmt.Errorf("databaseset: open primary: %w", err)
	}
	observability, err := NewSQLiteDB(filepath.Dir(cfg.ObservabilityDBPath), filepath.Base(cfg.ObservabilityDBPath), log)
	if err != nil {
		_ = primary.Close()
		return nil, fmt.Errorf("databaseset: open observability: %w", err)
	}

	log.Info("DatabaseSet opened",
		zap.String("primary", primary.Path()),
		zap.String("observability", observability.Path()),
	)

	return &DatabaseSet{
		Primary:       primary,
		Observability: observability,
		cfg:           cfg,
		log:           log,
	}, nil
}

// Migrate runs the canonical migration ledger against BOTH the Primary
// and Observability databases. Migrations are read from
// `migrations/sqlite/`; the directory is shared across both DBs (each
// migration file's first line must declare its target DB via the
// `-- database: primary|observability` header convention introduced in
// this PR). Errors on either DB roll forward as-is — there is no
// distributed transaction across both.
func (s *DatabaseSet) Migrate(log *zap.Logger) error {
	if log == nil {
		log = s.log
	}
	if s.closed.Load() {
		return fmt.Errorf("databaseset: already closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// scope-aware migrations (June 2026) — each DB receives its
	// canonical scope target ("primary" / "observability"); the runner
	// skips out-of-scope migrations before the checksum check, so
	// migration 109 (ALTER TABLE media_assets …) is never attempted
	// against the observability DB.
	migrationsDir := resolveMigrationsDir()
	if err := s.Primary.RunMigrations(log, migrationsDir, "primary"); err != nil {
		return fmt.Errorf("databaseset: migrate primary: %w", err)
	}
	if err := s.Observability.RunMigrations(log, migrationsDir, "observability"); err != nil {
		return fmt.Errorf("databaseset: migrate observability: %w", err)
	}
	log.Info("DatabaseSet migrations applied",
		zap.String("primary", s.Primary.Path()),
		zap.String("observability", s.Observability.Path()),
	)
	return nil
}

func resolveMigrationsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("migrations", "sqlite")
	}
	dir := filepath.Dir(file)
	for {
		candidate := filepath.Join(dir, "migrations", "sqlite")
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join("migrations", "sqlite")
}

// Health runs `PRAGMA quick_check` on BOTH databases. Returns the
// first error encountered (primary first, then observability) so the
// caller can log a structured failure.
func (s *DatabaseSet) Health(ctx context.Context) error {
	if s.closed.Load() {
		return fmt.Errorf("databaseset: already closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := quickCheck(ctx, s.Primary.DB); err != nil {
		return fmt.Errorf("databaseset: primary health: %w", err)
	}
	if err := quickCheck(ctx, s.Observability.DB); err != nil {
		return fmt.Errorf("databaseset: observability health: %w", err)
	}
	return nil
}

// quickCheck runs PRAGMA quick_check which returns ok/integer-coded error.
func quickCheck(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("nil db handle")
	}
	var status string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&status); err != nil {
		return err
	}
	if status != "ok" {
		return fmt.Errorf("quick_check returned %q", status)
	}
	return nil
}

// Close closes BOTH databases in idempotent fashion. Safe to call
// multiple times — the second call is a no-op.
func (s *DatabaseSet) Close() error {
	if !s.closed.CompareAndSwap(false, true) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var firstErr error
	if s.Primary != nil {
		if err := s.Primary.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("databaseset: close primary: %w", err)
		}
	}
	if s.Observability != nil {
		if err := s.Observability.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("databaseset: close observability: %w", err)
		}
	}
	return firstErr
}

// Config returns the resolved StorageConfig the set was opened with.
func (s *DatabaseSet) Config() StorageConfig { return s.cfg }

// PrimaryPath returns the absolute filesystem path to the primary DB.
func (s *DatabaseSet) PrimaryPath() string {
	if s.Primary == nil {
		return ""
	}
	return s.Primary.Path()
}

// ObservabilityPath returns the absolute filesystem path to the
// observability DB.
func (s *DatabaseSet) ObservabilityPath() string {
	if s.Observability == nil {
		return ""
	}
	return s.Observability.Path()
}
