// cmd/admin/db_doctor.go (DB-08, 2026-11-27):
//
// `admin db doctor` is the read-only gate that ties together the checks the
// audit found missing: SQLite integrity/FK/ledger/retention/allowlist, and
// PostgreSQL FK/invalid-index/dead-tuple checks. Errors always exit non-zero;
// warnings do not block the boot unless --strict is set.
//
// Two Prometheus families are populated by the checks — db_fk_violations and
// db_dead_tuples (internal/platform/dbhealth/metrics.go) — and --metrics-out
// writes them in Prometheus text format. The nightly workflow runs the gate
// against the media PostgreSQL with the DSN taken from a repository secret and
// uploads that snapshot as an artifact.
package database

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/dbhealth"
	pgmediaops "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/mediaops"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	"go.uber.org/zap"
)

func RunDBDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("db doctor", flag.ExitOnError)
	dataDir := fs.String("data-dir", "./data", "root data directory")
	strict := fs.Bool("strict", false, "treat warnings as failures")
	quick := fs.Bool("quick", false, "use PRAGMA quick_check instead of integrity_check")
	skipSQLite := fs.Bool("skip-sqlite", false, "skip the SQLite planes; run only the PostgreSQL checks (nightly runner has no data directory)")
	metricsOut := fs.String("metrics-out", "", "write db_fk_violations and db_dead_tuples to this file in Prometheus text format")
	pgDSN := fs.String("postgres-dsn", "", "PostgreSQL media DSN (default PIPELINEGEN_MEDIA_POSTGRES_DSN)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	// A second in-process run must not keep labels from the first pass.
	dbhealth.DBFKViolations.Reset()
	dbhealth.DBDeadTuples.Reset()

	var findings []dbhealth.Finding

	if !*skipSQLite {
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

		// A gate run right after a retention sweep must not report the rows that
		// crossed the moving cutoff in between: subtract a small grace period,
		// then measure. Without it the same boundary row flips the exit code on
		// every back-to-back `retention && doctor`.
		const retentionGateGrace = 15 * time.Minute
		telemetryCutoff := time.Now().UTC().AddDate(0, 0, -fullCfg.Storage.ObservabilityMaxAgeDays).Add(-retentionGateGrace).Format("2006-01-02 15:04:05")
		historyCutoff := time.Now().UTC().AddDate(0, 0, -fullCfg.Jobs.RetentionDays).Add(-retentionGateGrace).Format("2006-01-02 15:04:05")
		plans := buildRetentionPlans(resolved.DataDir(), resolved.ObservabilityDBPath(), mustParseCutoff(telemetryCutoff), mustParseCutoff(historyCutoff))
		retentionByPlane := map[string][]storage.RetentionCheck{}
		for _, p := range plans {
			for _, op := range p.Ops {
				cutoff := telemetryCutoff
				if op.Cutoff.Equal(mustParseCutoff(historyCutoff)) {
					cutoff = historyCutoff
				}
				retentionByPlane[p.Name] = append(retentionByPlane[p.Name], storage.RetentionCheck{
					Table: op.Table, TimeColumn: op.TimeColumn, Cutoff: cutoff,
					Remediation: "DB-04/DB-05: run `admin db retention`",
				})
			}
		}

		appendPlane := func(plane string, sdb *storage.SQLiteDB, dir, allowlistPlane string, retention []storage.RetentionCheck) {
			if sdb == nil {
				return
			}
			findings = append(findings, storage.RunSQLiteDoctor(ctx, sdb.DB, storage.DoctorOptions{
				Plane:     plane,
				DBPath:    sdb.Path(),
				Quick:     *quick,
				LedgerDir: dir,
				Target:    plane,
				Retention: retention,
				AllowedTables: func() map[string]bool {
					switch allowlistPlane {
					case "observability":
						return observabilityTableAllowlist()
					case "cache":
						return cacheTableAllowlist()
					default:
						return nil
					}
				}(),
			})...)
		}

		migrationsDir := storage.MigrationsDir()
		appendPlane("primary", ds.Primary, migrationsDir, "primary", retentionByPlane["media"])
		appendPlane("observability", ds.Observability, migrationsDir, "observability", retentionByPlane["observability"])
		appendPlane("cache", ds.Cache, migrationsDir, "cache", nil)
		appendPlane("jobs", ds.Jobs, storage.JobsMigrationsDir(), "jobs", retentionByPlane["jobs"])
	}

	dsn := *pgDSN
	if dsn == "" {
		dsn = os.Getenv("PIPELINEGEN_MEDIA_POSTGRES_DSN")
	}
	if dsn == "" {
		findings = append(findings, dbhealth.Finding{
			Plane: "postgres/media", Check: "connection", Severity: dbhealth.SeverityWarning,
			Detail: "PIPELINEGEN_MEDIA_POSTGRES_DSN not set; PostgreSQL checks skipped", Remediation: "DB-08: set the DSN in nightly/CI",
		})
	} else {
		pg, err := sql.Open("pgx", dsn)
		if err != nil {
			findings = append(findings, dbhealth.Finding{Plane: "postgres/media", Check: "connection", Severity: dbhealth.SeverityError, Detail: err.Error(), Remediation: "DB-08"})
		} else {
			defer func() { _ = pg.Close() }()
			pgFindings, err := pgmediaops.DoctorFindings(ctx, pg)
			if err != nil {
				findings = append(findings, dbhealth.Finding{Plane: "postgres/media", Check: "doctor", Severity: dbhealth.SeverityError, Detail: err.Error(), Remediation: "DB-08"})
			} else {
				findings = append(findings, pgFindings...)
			}
		}
	}

	if *metricsOut != "" {
		if err := writeDoctorMetrics(*metricsOut); err != nil {
			return err
		}
	}

	for _, f := range findings {
		fmt.Println(f.String())
	}
	if dbhealth.HasBlocking(findings, *strict) {
		return fmt.Errorf("db doctor: blocking findings (strict=%v)", *strict)
	}
	return nil
}

// writeDoctorMetrics exports only the two doctor families: the nightly artifact
// is a health snapshot, not a copy of the whole process registry.
func writeDoctorMetrics(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("metrics-out: %w", err)
	}
	defer func() { _ = f.Close() }()

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return fmt.Errorf("metrics-out: gather: %w", err)
	}
	enc := expfmt.NewEncoder(f, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, mf := range families {
		switch mf.GetName() {
		case "db_fk_violations", "db_dead_tuples":
			if err := enc.Encode(mf); err != nil {
				return fmt.Errorf("metrics-out: encode %s: %w", mf.GetName(), err)
			}
		}
	}
	return f.Sync()
}

func mustParseCutoff(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		return time.Time{}
	}
	return t
}
