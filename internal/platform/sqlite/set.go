// Package storage — DatabaseSet: the canonical entry point for opening,
// migrating, health-checking, and closing ALL sqlite databases used by
// the PipelineGen runtime.
//
// Pre-2026 the codebase opened a single sqlite DB at
// `<DataDir>/media/media.db.sqlite` plus a secondary DB at `<DataDir>/logs/api_requests.db.sqlite`
// from inside `internal/app/bootstrap.go`. Both open-sites used
// `database/sql` directly. This file centralises that pattern so callers
// (notably `internal/app/composition.go`) can open the full set with one
// typed call and never see a raw `*sql.DB` outside this package.
//
// The set is split into two roles:
//
//   - Primary       — the canonical media database (assets, scripts,
//     media_assets, clip_folders, search_queries, worker_nodes, voiceovers,
//     and media outbox).
//     historically at `<DataDir>/media/media.db.sqlite`.
//   - Observability — the API request log database at
//     `<DataDir>/observability/api_requests.db.sqlite`.
//
// Defaults use the canonical unified path
// `<DataDir>/media/media.db.sqlite`. Legacy single-file paths are rejected
// before any runtime handle is opened. The path migration tool
// (`cmd/admin/path_migrate.go`, future PR) performs backup + SHA256
// checksum + PRAGMA integrity_check + rollback when operators opt in.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	// `internal/platform/sqlite/<owner>/`.
	Primary *SQLiteDB

	// Observability holds the API request log database
	// (`api_requests` table + indexes). Independent file from Primary
	// so log retention doesn't churn the schema-versioned Primary DB.
	Observability *SQLiteDB

	// Cache holds rebuildable cache state. Cache failures are intentionally
	// non-fatal to business-state startup and callers must treat them as misses.
	Cache *SQLiteDB

	// Jobs holds the optional execution-plane database. It is opened only when
	// the composition root explicitly enables the jobs split.
	Jobs *SQLiteDB

	cfg StorageConfig
	log *zap.Logger

	mu     sync.Mutex
	closed atomic.Bool
}

// StorageConfig is the resolved storage layout passed to OpenSet. It
// mirrors `config.StorageConfig` but is package-local to avoid an
// `internal/platform/sqlite` → `internal/platform/config`
// import cycle when this package is consumed by `config`.
type StorageConfig struct {
	DataDir             string
	ObservabilityDBPath string
	CacheDBPath         string
	JobsDBPath          string
	WorkspaceDir        string
	CacheDir            string
	ExportDir           string
}

// ResolveStorageConfig fills zero-valued paths in `cfg` with the canonical
// operational layout. The primary database path is derived exclusively from
// DataDir and cannot be overridden.
func ResolveStorageConfig(cfg StorageConfig) StorageConfig {
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if cfg.ObservabilityDBPath == "" {
		cfg.ObservabilityDBPath = filepath.Join(cfg.DataDir, "observability", "api_requests.db.sqlite")
	}
	if cfg.CacheDBPath == "" {
		cfg.CacheDBPath = filepath.Join(cfg.DataDir, "cache", "cache.db.sqlite")
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

// OpenSet opens the Primary and Observability databases, plus the optional
// Cache and Jobs planes, with the
// canonical wal/foreign_keys/busy_timeout pragma set, runs a Ping on
// each, and returns the typed DatabaseSet. This is the ONLY entry point
// in the runtime that creates a `*sql.DB` — every other consumer calls
// `set.Primary` or `set.Observability`.
func OpenSet(cfg StorageConfig, log *zap.Logger) (*DatabaseSet, error) {
	if log == nil {
		log = zap.NewNop()
	}
	cfg = ResolveStorageConfig(cfg)
	primaryPath, err := filepath.Abs(filepath.Join(cfg.DataDir, "media", "media.db.sqlite"))
	if err != nil {
		return nil, fmt.Errorf("databaseset: resolve primary SQLite path: %w", err)
	}

	primary, err := NewSQLiteDB(filepath.Dir(primaryPath), filepath.Base(primaryPath), log)
	if err != nil {
		return nil, fmt.Errorf("databaseset: open primary: %w", err)
	}
	observability, err := NewSQLiteDB(filepath.Dir(cfg.ObservabilityDBPath), filepath.Base(cfg.ObservabilityDBPath), log)
	if err != nil {
		_ = primary.Close()
		return nil, fmt.Errorf("databaseset: open observability: %w", err)
	}
	// Cache is deliberately best-effort: it is derived state, so a bad or
	// temporarily unavailable cache path must degrade to misses rather than
	// prevent the canonical media plane from starting.
	var cache *SQLiteDB
	cache, err = NewSQLiteDB(filepath.Dir(cfg.CacheDBPath), filepath.Base(cfg.CacheDBPath), log)
	if err != nil {
		log.Warn("DatabaseSet cache unavailable; continuing with cache misses", zap.Error(err), zap.String("cache", cfg.CacheDBPath))
		cache = nil
	}

	// The set topology is fixed at the composition boundary: Primary is the
	// sole writable Control Plane database; Observability is an operational
	// log store and is deliberately not part of the Control Plane writer set.
	// Validate this before migrations or services can be started.
	databases := []ConfiguredDatabase{
		{Name: "primary", Path: primary.Path(), Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
		{Name: "observability", Path: observability.Path(), Role: ControlPlaneRoleReadOnly, Writable: true, ControlPlane: false},
	}
	if cache != nil {
		databases = append(databases, ConfiguredDatabase{Name: "cache", Path: cache.Path(), Role: ControlPlaneRoleReadOnly, Writable: true, ControlPlane: false})
	}
	if err := ValidateConfiguredControlPlaneWriters(databases); err != nil {
		if cache != nil {
			_ = cache.Close()
		}
		_ = observability.Close()
		_ = primary.Close()
		return nil, fmt.Errorf("databaseset: validate control-plane topology: %w", err)
	}

	var jobs *SQLiteDB
	if strings.TrimSpace(cfg.JobsDBPath) != "" {
		jobs, err = NewSQLiteDB(filepath.Dir(cfg.JobsDBPath), filepath.Base(cfg.JobsDBPath), log)
		if err != nil {
			if cache != nil {
				_ = cache.Close()
			}
			_ = observability.Close()
			_ = primary.Close()
			return nil, fmt.Errorf("databaseset: open jobs: %w", err)
		}
	}

	cachePath := "unavailable (cache miss mode)"
	if cache != nil {
		cachePath = cache.Path()
	}
	log.Info("DatabaseSet opened",
		zap.String("primary", primary.Path()),
		zap.String("observability", observability.Path()),
		zap.String("cache", cachePath),
		zap.String("control_plane_role", string(ControlPlaneRoleCanonical)),
	)

	return &DatabaseSet{
		Primary:       primary,
		Observability: observability,
		Cache:         cache,
		Jobs:          jobs,
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

	// Scope-aware migrations: each DB receives its
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
	if s.Cache != nil {
		if err := s.Cache.RunMigrations(log, migrationsDir, "cache"); err != nil {
			return fmt.Errorf("databaseset: migrate cache: %w", err)
		}
	}
	if s.Jobs != nil {
		if err := s.Jobs.RunMigrations(log, resolveJobsMigrationsDir(), "jobs"); err != nil {
			return fmt.Errorf("databaseset: migrate jobs: %w", err)
		}
	}
	if err := s.ValidateControlPlaneIdentity(context.Background()); err != nil {
		return fmt.Errorf("databaseset: validate control-plane identity: %w", err)
	}
	log.Info("DatabaseSet migrations applied",
		zap.String("primary", s.Primary.Path()),
		zap.String("observability", s.Observability.Path()),
		zap.String("control_plane_role", string(ControlPlaneRoleCanonical)),
	)
	return nil
}

// ValidateControlPlaneIdentity verifies that the migrated primary database is
// the one canonical writable SSOT. It is intentionally separate from OpenSet
// because migration 198 creates the durable identity during Migrate.
func (s *DatabaseSet) ValidateControlPlaneIdentity(ctx context.Context) error {
	if s == nil || s.Primary == nil || s.Primary.DB == nil {
		return errors.New("databaseset: primary control-plane database is not configured")
	}
	meta, err := ReadControlPlaneMeta(ctx, s.Primary.DB)
	if err != nil {
		return err
	}
	if meta.InstanceRole != ControlPlaneRoleCanonical {
		return fmt.Errorf("databaseset: primary database_id=%q has role %q, want %q", meta.DatabaseID, meta.InstanceRole, ControlPlaneRoleCanonical)
	}

	databases := []ConfiguredDatabase{{
		Name:         "primary",
		Path:         s.Primary.Path(),
		Role:         meta.InstanceRole,
		Writable:     true,
		ControlPlane: true,
	}}
	if s.Observability != nil {
		databases = append(databases, ConfiguredDatabase{
			Name:         "observability",
			Path:         s.Observability.Path(),
			Role:         ControlPlaneRoleReadOnly,
			Writable:     true,
			ControlPlane: false,
		})
	}
	return ValidateConfiguredControlPlaneWriters(databases)
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

func resolveJobsMigrationsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return filepath.Join("migrations", "sqlite_jobs")
	}
	dir := filepath.Dir(file)
	for {
		candidate := filepath.Join(dir, "migrations", "sqlite_jobs")
		if stat, err := os.Stat(candidate); err == nil && stat.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join("migrations", "sqlite_jobs")
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
	if s.Cache != nil {
		if err := quickCheck(ctx, s.Cache.DB); err != nil {
			return fmt.Errorf("databaseset: cache health: %w", err)
		}
	}
	if s.Jobs != nil {
		if err := quickCheck(ctx, s.Jobs.DB); err != nil {
			return fmt.Errorf("databaseset: jobs health: %w", err)
		}
	}
	return nil
}

// PlaneHealth is the independent health result for one storage plane. A
// degraded cache or observability plane is reported without masking the
// canonical media/jobs result.
type PlaneHealth struct {
	Available bool
	Error     error
}

// HealthByPlane checks every configured SQLite plane independently. Unlike
// Health, this method never stops at the first failure and is intended for
// readiness surfaces that distinguish CORE, EXECUTION, DERIVED and
// OBSERVABILITY availability.
func (s *DatabaseSet) HealthByPlane(ctx context.Context) map[string]PlaneHealth {
	result := map[string]PlaneHealth{}
	if s == nil || s.closed.Load() {
		return result
	}
	check := func(name string, db *SQLiteDB) {
		if db == nil || db.DB == nil {
			result[name] = PlaneHealth{Error: fmt.Errorf("%s database unavailable", name)}
			return
		}
		if err := quickCheck(ctx, db.DB); err != nil {
			result[name] = PlaneHealth{Error: err}
			return
		}
		result[name] = PlaneHealth{Available: true}
	}
	check("media", s.Primary)
	check("jobs", s.Jobs)
	check("cache", s.Cache)
	check("observability", s.Observability)
	return result
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

// Close closes all configured databases in idempotent fashion. Safe to call
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
	if s.Cache != nil {
		if err := s.Cache.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("databaseset: close cache: %w", err)
		}
	}
	if s.Jobs != nil {
		if err := s.Jobs.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("databaseset: close jobs: %w", err)
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
