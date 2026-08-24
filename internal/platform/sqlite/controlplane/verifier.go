// Package controlplane verifies the SQLite Control Plane without mutating it.
package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
)

type Verifier struct {
	db       *sql.DB
	path     string
	topology []storage.ConfiguredDatabase
}

// MAX(version) is insufficient: a ledger containing 193 and 195 has a
// plausible maximum while still missing a required schema transition.
var requiredMigrations = []int{194, 197, 198, 199, 200, 202, 203, 206}

func New(db *sql.DB, path string) (*Verifier, error) {
	return NewWithTopology(db, path, nil)
}

// NewWithTopology adds the configured database inventory to the deep
// verifier. The single-database New constructor remains available for
// focused callers, while production verification can now certify both the
// durable identity and the one-writable-Control-Plane topology.
func NewWithTopology(db *sql.DB, path string, topology []storage.ConfiguredDatabase) (*Verifier, error) {
	if db == nil {
		return nil, fmt.Errorf("control plane verifier: nil database")
	}
	if path == "" {
		return nil, fmt.Errorf("control plane verifier: empty database path")
	}
	return &Verifier{db: db, path: filepath.Clean(path), topology: append([]storage.ConfiguredDatabase(nil), topology...)}, nil
}

func (v *Verifier) Verify(ctx context.Context) (capcontrol.Report, error) {
	r := capcontrol.Report{CanonicalDBPath: v.path, RegistryID: "sqlite:" + v.path, Status: "HEALTHY", ProjectionState: "UNKNOWN"}
	add := func(name, status, detail string) {
		r.Checks = append(r.Checks, capcontrol.Check{Name: name, Status: status, Detail: detail})
		if status != "PASS" {
			r.Status = "FAILED"
		}
	}

	var integrity string
	if err := v.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		add("integrity_check", "FAIL", err.Error())
	} else if integrity != "ok" {
		add("integrity_check", "FAIL", integrity)
	} else {
		add("integrity_check", "PASS", "ok")
	}

	var version int
	if err := v.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		add("schema_version", "FAIL", err.Error())
	} else {
		r.SchemaVersion = version
		add("schema_version", "PASS", fmt.Sprintf("%d", version))
	}
	for _, required := range requiredMigrations {
		var filename, checksum string
		if err := v.db.QueryRowContext(ctx, `SELECT filename, checksum FROM schema_migrations WHERE version=?`, required).Scan(&filename, &checksum); err != nil {
			r.MigrationGaps = append(r.MigrationGaps, required)
			add(fmt.Sprintf("migration:%03d", required), "FAIL", "required migration is missing from ledger")
			continue
		}
		currentFilename, content, err := currentMigration(required)
		if err != nil {
			add(fmt.Sprintf("migration:%03d", required), "FAIL", err.Error())
			continue
		}
		currentChecksum := sha256Hex(content)
		if filename != currentFilename || checksum != currentChecksum {
			add(fmt.Sprintf("migration:%03d", required), "FAIL", fmt.Sprintf("ledger filename/checksum mismatch: ledger=%s/%s current=%s/%s", filename, checksum, currentFilename, currentChecksum))
			continue
		}
		add(fmt.Sprintf("migration:%03d", required), "PASS", "applied with matching filename and checksum")
	}

	required := []string{"control_plane_meta", "media_assets", "asset_text_tracks", "jobs", "job_steps", "registry_events", "registry_runs", "projection_registry", "backup_registry", "outbox_events", "content_objects", "media_asset_sources", "source_identity_registry", "canonical_mutations", "performance_runs", "performance_steps", "performance_artifacts", "benchmark_workloads"}
	for _, table := range required {
		var n int
		err := v.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n)
		if err != nil || n != 1 {
			add("table:"+table, "FAIL", "required canonical table is missing")
			continue
		}
		add("table:"+table, "PASS", "present")
	}
	if len(v.topology) > 0 {
		if err := storage.ValidateConfiguredControlPlaneWriters(v.topology); err != nil {
			add("control_plane_topology", "FAIL", err.Error())
		} else {
			add("control_plane_topology", "PASS", "exactly one writable canonical Control Plane database")
		}
	}

	meta, err := storage.ReadControlPlaneMeta(ctx, v.db)
	if err != nil {
		add("control_plane_identity", "FAIL", err.Error())
	} else if meta.InstanceRole != storage.ControlPlaneRoleCanonical {
		add("control_plane_identity", "FAIL", fmt.Sprintf("database_id=%q schema_family=%q role=%q", meta.DatabaseID, meta.SchemaFamily, meta.InstanceRole))
	} else {
		r.DatabaseID = meta.DatabaseID
		r.SchemaFamily = meta.SchemaFamily
		r.InstanceRole = string(meta.InstanceRole)
		add("control_plane_identity", "PASS", fmt.Sprintf("database_id=%s role=%s", meta.DatabaseID, meta.InstanceRole))
	}

	queries := []struct {
		name, sql string
		dst       *int64
	}{
		{"assets", `SELECT COUNT(*) FROM media_assets WHERE lifecycle_state='ACTIVE'`, &r.Assets},
		{"transcripts", `SELECT COUNT(DISTINCT asset_id) FROM asset_text_tracks WHERE text_kind='transcript' AND status='READY' AND is_current=1`, &r.Transcripts},
		{"descriptions", `SELECT COUNT(DISTINCT asset_id) FROM asset_text_tracks WHERE text_kind IN ('description','summary') AND status='READY' AND is_current=1`, &r.Descriptions},
		{"jobs", `SELECT COUNT(*) FROM jobs`, &r.Jobs},
		{"pending_outbox", `SELECT COUNT(*) FROM outbox_events WHERE lower(status) IN ('pending','retry','processing')`, &r.PendingOutbox},
		{"dead_outbox", `SELECT COUNT(*) FROM outbox_events WHERE upper(status)='DEAD'`, &r.DeadOutbox},
		{"cas_objects", `SELECT COUNT(*) FROM content_objects`, &r.CASObjects},
		{"cas_orphans", `SELECT COUNT(*) FROM content_objects c LEFT JOIN media_assets a ON a.content_sha256=c.sha256 LEFT JOIN media_asset_sources s ON s.content_sha256=c.sha256 WHERE a.id IS NULL AND s.source_id IS NULL`, &r.CASOrphans},
		{"broken_cas_links", `SELECT COUNT(*) FROM media_assets a LEFT JOIN content_objects c ON c.sha256=a.content_sha256 WHERE COALESCE(a.content_sha256,'') <> '' AND c.sha256 IS NULL`, &r.BrokenCASLinks},
		{"registry_seq", `SELECT COALESCE(MAX(seq),0) FROM registry_events`, &r.RegistrySeq},
		{"projection_seq", `SELECT COALESCE(MAX(source_registry_seq),0) FROM projection_registry WHERE status='ACTIVE'`, &r.ProjectionSeq},
		{"performance_runs", `SELECT COUNT(*) FROM performance_runs`, &r.PerformanceRuns},
		{"uncorrelated_performance_runs", `SELECT COUNT(*) FROM performance_runs WHERE job_id='' OR root_job_id=''`, &r.UncorrelatedPerformanceRuns},
	}
	for _, q := range queries {
		if err := v.db.QueryRowContext(ctx, q.sql).Scan(q.dst); err != nil {
			add("metric:"+q.name, "FAIL", err.Error())
		}
	}
	r.ProjectionDrift = r.RegistrySeq - r.ProjectionSeq
	if r.ProjectionDrift < 0 {
		r.ProjectionDrift = -r.ProjectionDrift
	}
	if r.DeadOutbox > 0 {
		add("outbox_dead_letter", "FAIL", fmt.Sprintf("%d dead events", r.DeadOutbox))
	} else {
		add("outbox_dead_letter", "PASS", "0")
	}
	if r.CASOrphans > 0 {
		add("cas_orphans", "FAIL", fmt.Sprintf("%d orphan content objects", r.CASOrphans))
	} else {
		add("cas_orphans", "PASS", "0")
	}
	if r.BrokenCASLinks > 0 {
		add("cas_links", "FAIL", fmt.Sprintf("%d broken asset links", r.BrokenCASLinks))
	} else {
		add("cas_links", "PASS", "0")
	}
	if r.UncorrelatedPerformanceRuns > 0 {
		add("performance_lineage", "FAIL", fmt.Sprintf("%d performance runs lack job/root correlation", r.UncorrelatedPerformanceRuns))
	} else {
		add("performance_lineage", "PASS", "all performance runs are correlated")
	}
	switch {
	case r.RegistrySeq == r.ProjectionSeq:
		r.ProjectionState = "SYNCHRONIZED"
		add("projection_sync", "PASS", "registry and active projection aligned")
	case r.ProjectionSeq < r.RegistrySeq:
		r.ProjectionState = "LAGGING"
		add("projection_sync", "FAIL", fmt.Sprintf("projection lag=%d", r.ProjectionDrift))
	default:
		r.ProjectionState = "AHEAD_OF_SSOT"
		add("projection_sync", "FAIL", fmt.Sprintf("projection ahead by=%d", r.ProjectionDrift))
	}
	return r, nil
}

// VerifyDeep extends the normal health check with an exact migration-ledger
// comparison. MAX(version) is deliberately not used as the certification
// criterion: every migration file must have one matching ledger row and every
// ledger row must still have a source file.
func (v *Verifier) VerifyDeep(ctx context.Context) (capcontrol.Report, error) {
	r, err := v.Verify(ctx)
	if err != nil {
		return r, err
	}
	add := func(name, status, detail string) {
		r.Checks = append(r.Checks, capcontrol.Check{Name: name, Status: status, Detail: detail})
		if status != "PASS" {
			r.Status = "FAILED"
		}
	}

	entries, err := os.ReadDir(migrationsDirectory())
	if err != nil {
		add("migration_ledger_exact", "FAIL", err.Error())
		return r, nil
	}
	files := make(map[int]struct{})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, ok := migrationVersion(entry.Name())
		if !ok {
			continue
		}
		files[version] = struct{}{}
		content, readErr := os.ReadFile(filepath.Join(migrationsDirectory(), entry.Name()))
		if readErr != nil {
			add(fmt.Sprintf("migration:%03d", version), "FAIL", readErr.Error())
			continue
		}
		var filename, checksum string
		queryErr := v.db.QueryRowContext(ctx, `SELECT filename, checksum FROM schema_migrations WHERE version=?`, version).Scan(&filename, &checksum)
		if queryErr == sql.ErrNoRows {
			r.MigrationGaps = appendUniqueInt(r.MigrationGaps, version)
			add(fmt.Sprintf("migration:%03d", version), "FAIL", "migration file is not recorded in schema_migrations")
			continue
		}
		if queryErr != nil {
			add(fmt.Sprintf("migration:%03d", version), "FAIL", queryErr.Error())
			continue
		}
		expected := sha256Hex(content)
		if filename != entry.Name() || checksum != expected {
			r.MigrationChecksumMismatches = appendUniqueInt(r.MigrationChecksumMismatches, version)
			add(fmt.Sprintf("migration:%03d", version), "FAIL", "filename or SHA-256 checksum mismatch")
		}
	}
	rows, queryErr := v.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if queryErr != nil {
		add("migration_ledger_exact", "FAIL", queryErr.Error())
		return r, nil
	}
	defer rows.Close()
	for rows.Next() {
		var version int
		if scanErr := rows.Scan(&version); scanErr != nil {
			add("migration_ledger_exact", "FAIL", scanErr.Error())
			return r, nil
		}
		if _, ok := files[version]; !ok {
			add(fmt.Sprintf("migration:%03d", version), "FAIL", "ledger row has no migration file")
		}
	}
	if err := rows.Err(); err != nil {
		add("migration_ledger_exact", "FAIL", err.Error())
		return r, nil
	}
	if len(r.MigrationGaps) == 0 && len(r.MigrationChecksumMismatches) == 0 {
		add("migration_ledger_exact", "PASS", fmt.Sprintf("%d migration files certified", len(files)))
	}
	return r, nil
}

func migrationsDirectory() string {
	_, sourceFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(sourceFile), "../../../../migrations/sqlite")
}

func migrationVersion(name string) (int, bool) {
	if len(name) < 4 || name[3] != '_' {
		return 0, false
	}
	var version int
	if _, err := fmt.Sscanf(name[:3], "%03d", &version); err != nil {
		return 0, false
	}
	return version, true
}

func appendUniqueInt(values []int, value int) []int {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func currentMigration(version int) (string, []byte, error) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", nil, errors.New("control plane verifier: cannot locate source tree")
	}
	dir := filepath.Join(filepath.Dir(sourceFile), "../../../../migrations/sqlite")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", nil, fmt.Errorf("control plane verifier: read migrations: %w", err)
	}
	prefix := fmt.Sprintf("%03d_", version)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return "", nil, fmt.Errorf("control plane verifier: read migration %s: %w", entry.Name(), err)
		}
		return entry.Name(), content, nil
	}
	return "", nil, fmt.Errorf("control plane verifier: migration file for version %03d is missing", version)
}

func sha256Hex(content []byte) string {
	return digest.SHA256Bytes(content)
}
