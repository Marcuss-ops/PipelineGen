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

	// The set topology is fixed at the composition boundary: Primary is the
	// sole writable Control Plane database; Observability is an operational
	// log store and is deliberately not part of the Control Plane writer set.
	// Validate this before migrations or services can be started.
	if err := ValidateConfiguredControlPlaneWriters([]ConfiguredDatabase{
		{Name: "primary", Path: primary.Path(), Role: ControlPlaneRoleCanonical, Writable: true, ControlPlane: true},
		{Name: "observability", Path: observability.Path(), Role: ControlPlaneRoleReadOnly, Writable: true, ControlPlane: false},
	}); err != nil {
		_ = observability.Close()
		_ = primary.Close()
		return nil, fmt.Errorf("databaseset: validate control-plane topology: %w", err)
	}

	log.Info("DatabaseSet opened",
		zap.String("primary", primary.Path()),
		zap.String("observability", observability.Path()),
		zap.String("control_plane_role", string(ControlPlaneRoleCanonical)),
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

// ControlPlaneRole identifies how a configured database participates in the
// control-plane topology. Only CANONICAL may be an operational writer.
type ControlPlaneRole string

const (
	ControlPlaneRoleCanonical       ControlPlaneRole = "CANONICAL"
	ControlPlaneRoleReadOnly        ControlPlaneRole = "READ_ONLY"
	ControlPlaneRoleMigrationSource ControlPlaneRole = "MIGRATION_SOURCE"
	ControlPlaneRoleArchive         ControlPlaneRole = "ARCHIVE"
)

const canonicalControlPlaneSchemaFamily = "pipelinegen-control-plane"

// ControlPlaneMeta is the durable identity stored in control_plane_meta.
type ControlPlaneMeta struct {
	DatabaseID       string
	SchemaFamily     string
	InstanceRole     ControlPlaneRole
	CanonicalVersion int
	CreatedAt        string
}

// ConfiguredDatabase describes a database known to the application when it
// evaluates the single-writer invariant. It deliberately contains topology
// metadata rather than a storage implementation so the policy is testable
// without opening a second SQLite handle.
type ConfiguredDatabase struct {
	Name         string
	Path         string
	Role         ControlPlaneRole
	Writable     bool
	ControlPlane bool
}

// ReadControlPlaneMeta reads and validates the singleton control-plane
// identity row. Missing, duplicated, or malformed metadata is a hard error.
func ReadControlPlaneMeta(ctx context.Context, db *sql.DB) (ControlPlaneMeta, error) {
	if db == nil {
		return ControlPlaneMeta{}, errors.New("control plane identity: nil database")
	}

	rows, err := db.QueryContext(ctx, `
		SELECT database_id, schema_family, instance_role, canonical_version, created_at
		FROM control_plane_meta`)
	if err != nil {
		return ControlPlaneMeta{}, fmt.Errorf("control plane identity: read metadata: %w", err)
	}
	defer rows.Close()

	var meta ControlPlaneMeta
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			return ControlPlaneMeta{}, errors.New("control plane identity: control_plane_meta must contain exactly one row")
		}
		if err := rows.Scan(&meta.DatabaseID, &meta.SchemaFamily, &meta.InstanceRole, &meta.CanonicalVersion, &meta.CreatedAt); err != nil {
			return ControlPlaneMeta{}, fmt.Errorf("control plane identity: scan metadata: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return ControlPlaneMeta{}, fmt.Errorf("control plane identity: iterate metadata: %w", err)
	}
	if count != 1 {
		return ControlPlaneMeta{}, fmt.Errorf("control plane identity: control_plane_meta rows=%d, want exactly 1", count)
	}
	if err := validateControlPlaneMeta(meta); err != nil {
		return ControlPlaneMeta{}, err
	}
	return meta, nil
}

func validateControlPlaneMeta(meta ControlPlaneMeta) error {
	if strings.TrimSpace(meta.DatabaseID) == "" {
		return errors.New("control plane identity: database_id is empty")
	}
	if meta.SchemaFamily != canonicalControlPlaneSchemaFamily {
		return fmt.Errorf("control plane identity: schema_family=%q, want %q", meta.SchemaFamily, canonicalControlPlaneSchemaFamily)
	}
	switch meta.InstanceRole {
	case ControlPlaneRoleCanonical, ControlPlaneRoleReadOnly, ControlPlaneRoleMigrationSource, ControlPlaneRoleArchive:
	default:
		return fmt.Errorf("control plane identity: unknown instance_role=%q", meta.InstanceRole)
	}
	if meta.CanonicalVersion <= 0 {
		return fmt.Errorf("control plane identity: canonical_version=%d, want positive version", meta.CanonicalVersion)
	}
	if strings.TrimSpace(meta.CreatedAt) == "" {
		return errors.New("control plane identity: created_at is empty")
	}
	return nil
}

// ValidateConfiguredControlPlaneWriters enforces exactly one writable
// CANONICAL control-plane database. It also rejects aliases of one physical
// SQLite file being configured as multiple writable databases.
func ValidateConfiguredControlPlaneWriters(databases []ConfiguredDatabase) error {
	if len(databases) == 0 {
		return errors.New("control plane identity: no configured databases")
	}

	for _, database := range databases {
		if strings.TrimSpace(database.Name) == "" {
			return errors.New("control plane identity: configured database has empty name")
		}
	}

	// Check physical aliases first so a same-file collision is diagnosed even
	// when the role declarations are also inconsistent.
	for i := range databases {
		if !databases[i].ControlPlane || !databases[i].Writable {
			continue
		}
		for j := i + 1; j < len(databases); j++ {
			if !databases[j].ControlPlane || !databases[j].Writable || !samePhysicalFile(databases[i].Path, databases[j].Path) {
				continue
			}
			return fmt.Errorf("multiple control-plane writers detected: writable databases %q and %q resolve to the same SQLite file", databases[i].Name, databases[j].Name)
		}
	}

	canonicalWriters := make([]string, 0, len(databases))
	for _, database := range databases {
		if database.ControlPlane && database.Writable {
			if database.Role != ControlPlaneRoleCanonical {
				return fmt.Errorf("control plane identity: writable Control Plane database %q has role %q, want %q", database.Name, database.Role, ControlPlaneRoleCanonical)
			}
			canonicalWriters = append(canonicalWriters, database.Name)
		}
	}
	if len(canonicalWriters) != 1 {
		return fmt.Errorf("multiple control-plane writers detected: writable CANONICAL databases=%s (want exactly one)", strings.Join(canonicalWriters, ", "))
	}
	return nil
}

func samePhysicalFile(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(filepath.Clean(left))
	rightAbs, rightErr := filepath.Abs(filepath.Clean(right))
	if leftErr == nil && rightErr == nil && leftAbs == rightAbs {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}
